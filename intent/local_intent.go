/*
 * Copyright 2026 The RuleGo Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package intent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rulego/rulego"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/components/base"
	"github.com/rulego/rulego-components-ai/embedding"
	"github.com/rulego/rulego/utils/el"
	"github.com/rulego/rulego/utils/maps"
	"github.com/rulego/rulego/utils/str"
	"gopkg.in/yaml.v3"
)

func init() {
	_ = rulego.Registry.Register(&LocalIntentNode{})
}

// LocalIntentConfiguration embedding-based local intent recognition configuration
type LocalIntentConfiguration struct {
	// API
	Url   string `json:"url" label:"API URL" desc:"Embedding API endpoint, e.g. http://localhost:8080/v1/embeddings for TEI or https://ai.gitee.com/v1/embeddings for cloud" required:"true"`
	Key   string `json:"key" label:"API Key" desc:"API key for embedding service. Empty for private deployments without auth"`
	Model string `json:"model" label:"Model" desc:"Model name, e.g. BAAI/bge-small-zh-v1.5 or Qwen3-Embedding-8B" required:"true"`

	// User input
	Input string `json:"input" label:"Input Expression" desc:"User input expression. Supports ${msg.key} and ${metadata.key}. Empty uses msg.GetData()"`

	// Intent config (one of two)
	Intents     []LocalIntent `json:"intents" label:"Intents" desc:"Inline intent list. Use this or intentsFile"`
	IntentsFile string        `json:"intentsFile" label:"Intents File" desc:"External YAML/JSON file path. Format: {\"intents\":[{\"name\":\"...\",\"description\":\"...\",\"examples\":[\"...\"]}]"`

	// Matching parameters
	Threshold     float64 `json:"threshold" label:"Threshold" desc:"Minimum cosine similarity score [0,1] to accept a match. Below this returns defaultIntent"`
	MinGap        float64 `json:"minGap" label:"Min Gap" desc:"Minimum score gap between best and second-best intent. Below this treats as ambiguous match and returns defaultIntent"`
	DefaultIntent string  `json:"defaultIntent" label:"Default Intent" desc:"Fallback intent when score is below threshold or match is ambiguous"`
}

// LocalIntent intent definition with example sentences
type LocalIntent struct {
	Name        string   `json:"name" label:"Intent Name" desc:"Unique intent name used as route relation type" required:"true"`
	Description string   `json:"description" label:"Description" desc:"Intent description, also participates in embedding matching" required:"true"`
	Examples    []string `json:"examples" label:"Examples" desc:"Example sentences for embedding matching, 3-10 recommended" required:"true"`
}

// intentsFileConfig 外部意图配置文件格式
type intentsFileConfig struct {
	Intents []LocalIntent `yaml:"intents" json:"intents"`
}

// LocalIntentNode 基于 Embedding 的本地意图识别节点
type LocalIntentNode struct {
	Config            LocalIntentConfiguration
	embeddingClient   *embedding.EmbeddingClient
	userInputTemplate el.Template
	hasVar            bool
	intentVectors     []embedding.VectorEntry // 预计算的意图向量
}

// Type 组件类型
func (x *LocalIntentNode) Type() string {
	return "ai/localIntent"
}

// New 创建新的组件实例
func (x *LocalIntentNode) New() types.Node {
	return &LocalIntentNode{
		Config: LocalIntentConfiguration{
			Threshold:     0.65,
			MinGap:        0.05,
			DefaultIntent: types.DefaultRelationType,
			Intents: []LocalIntent{
				{
					Name: "createRule", Description: "创建条件触发的自动化联动规则",
					Examples: []string{
						"有人就开灯", "温度大于30度开空调", "水浸时开风机",
						"下雨天自动关窗", "离开家的时候关掉所有电器", "每天早上7点开窗帘",
						"空气质量差就开净化器", "燃气泄漏立刻关阀门",
					},
				},
				{
					Name: "control", Description: "控制设备开关或调节参数",
					Examples: []string{
						"打开灯光", "把风机关闭", "关闭客厅灯",
						"空调调到26度", "让窗帘拉下来", "把门锁上",
						"电视声音大一点", "关掉所有灯", "启动扫地机器人",
					},
				},
				{
					Name: "query", Description: "查询设备当前状态或数值",
					Examples: []string{
						"当前温度多少", "灯是不是开着的", "风机状态怎么样",
						"空调现在几度", "窗帘拉开着吗", "门锁了没",
						"现在湿度多少", "热水器还在加热吗",
					},
				},
			},
		},
	}
}

// Init 初始化
func (x *LocalIntentNode) Init(ruleConfig types.Config, configuration types.Configuration) error {
	if err := maps.Map2Struct(configuration, &x.Config); err != nil {
		return err
	}

	// 验证必填项
	x.Config.Url = strings.TrimSpace(x.Config.Url)
	if x.Config.Url == "" {
		return fmt.Errorf("url is required")
	}

	x.Config.Model = strings.TrimSpace(x.Config.Model)
	if x.Config.Model == "" {
		return fmt.Errorf("model is required")
	}

	// 加载意图配置
	intents, err := x.loadIntents()
	if err != nil {
		return err
	}
	if len(intents) == 0 {
		return fmt.Errorf("at least one intent must be defined (via intents or intentsFile)")
	}

	// 初始化用户输入模板（在 API 调用之前验证，快速失败）
	x.Config.Input = strings.TrimSpace(x.Config.Input)
	if x.Config.Input != "" {
		tmpl, err := el.NewTemplate(x.Config.Input)
		if err != nil {
			return fmt.Errorf("invalid input expression: %v", err)
		}
		x.userInputTemplate = tmpl
		if tmpl.HasVar() {
			x.hasVar = true
		}
	}

	// 初始化 Embedding 客户端
	x.embeddingClient = embedding.NewEmbeddingClient(
		x.Config.Url,
		x.Config.Key,
		x.Config.Model,
	)

	// 预计算意图向量
	if err := x.precomputeVectors(intents); err != nil {
		return fmt.Errorf("failed to precompute intent vectors: %v", err)
	}

	return nil
}

// OnMsg 处理消息
func (x *LocalIntentNode) OnMsg(ctx types.RuleContext, msg types.RuleMsg) {
	var evn map[string]interface{}
	if x.hasVar {
		evn = base.NodeUtils.GetEvnAndMetadata(ctx, msg)
	}

	// 获取用户输入文本
	var userInput string
	if x.userInputTemplate != nil {
		if v, err := x.userInputTemplate.Execute(evn); err != nil {
			ctx.TellFailure(msg, fmt.Errorf("failed to execute input template: %v", err))
			return
		} else {
			userInput = str.ToString(v)
		}
	} else {
		userInput = msg.GetData()
	}

	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		ctx.TellFailure(msg, fmt.Errorf("empty user input"))
		return
	}

	// 计算用户输入的 embedding
	vectors, err := x.embeddingClient.Embed(ctx.GetContext(), []string{userInput})
	if err != nil {
		ctx.TellFailure(msg, fmt.Errorf("failed to compute embedding: %v", err))
		return
	}
	if len(vectors) == 0 {
		ctx.TellFailure(msg, fmt.Errorf("empty embedding response"))
		return
	}

	// 在意图级别匹配：每个意图取最高分
	intentScores := x.intentTopScores(vectors[0])
	if len(intentScores) == 0 {
		ctx.TellFailure(msg, fmt.Errorf("no intent vectors available"))
		return
	}

	matchedIntent := intentScores[0].Name
	bestScore := intentScores[0].Score
	intentGap := bestScore
	if len(intentScores) >= 2 {
		intentGap = intentScores[0].Score - intentScores[1].Score
	}

	// 绝对分数低于阈值 或 与第二名意图差距不足 → 视为不确定
	relationType := matchedIntent
	if bestScore < x.Config.Threshold || (x.Config.MinGap > 0 && intentGap < x.Config.MinGap) {
		matchedIntent = x.Config.DefaultIntent
		relationType = types.DefaultRelationType
	}

	// 将结果写入 metadata 并路由
	msg.GetMetadata().PutValue(IntentMetadataKey, matchedIntent)
	ctx.TellNext(msg, relationType)
}

// Destroy 销毁资源
func (x *LocalIntentNode) Destroy() {
	// 无需清理
}

// intentScore 意图级别分数
type intentScore struct {
	Name  string
	Score float64
}

// intentTopScores 计算每个意图的最高相似度分数，按分数降序返回
func (x *LocalIntentNode) intentTopScores(target []float64) []intentScore {
	best := make(map[string]float64)
	for _, entry := range x.intentVectors {
		s := embedding.CosineSimilarity(target, entry.Vector)
		if s > best[entry.Name] {
			best[entry.Name] = s
		}
	}
	scores := make([]intentScore, 0, len(best))
	for name, score := range best {
		scores = append(scores, intentScore{Name: name, Score: score})
	}
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].Score > scores[j].Score
	})
	return scores
}

// Desc returns the component description
func (x *LocalIntentNode) Desc() string {
	return "Classify user intent via embedding cosine similarity and route to matching connection. Fast low-cost alternative to LLM-based intent recognition. Pre-computes intent vectors at init, matches at runtime. Routes to matched intent name or default"
}

// loadIntents 加载意图配置，支持内联和文件两种方式
func (x *LocalIntentNode) loadIntents() ([]LocalIntent, error) {
	// 优先使用内联配置
	if len(x.Config.Intents) > 0 {
		return x.Config.Intents, nil
	}

	// 从文件加载
	x.Config.IntentsFile = strings.TrimSpace(x.Config.IntentsFile)
	if x.Config.IntentsFile == "" {
		return nil, nil
	}

	data, err := os.ReadFile(x.Config.IntentsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read intents file '%s': %v", x.Config.IntentsFile, err)
	}

	var config intentsFileConfig
	ext := strings.ToLower(filepath.Ext(x.Config.IntentsFile))
	if ext == ".yaml" || ext == ".yml" {
		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("failed to parse YAML intents file: %v", err)
		}
	} else {
		if err := parseJSONIntents(data, &config); err != nil {
			return nil, fmt.Errorf("failed to parse JSON intents file: %v", err)
		}
	}

	if len(config.Intents) == 0 {
		return nil, fmt.Errorf("no intents found in file '%s'", x.Config.IntentsFile)
	}

	return config.Intents, nil
}

// precomputeVectors 预计算所有意图示例的 embedding 向量
func (x *LocalIntentNode) precomputeVectors(intents []LocalIntent) error {
	// 收集所有文本：每个 intent 的 description + examples
	var texts []string
	var textToIntent []string // 与 texts 一一对应的 intent name

	for _, intent := range intents {
		// description 也参与 embedding 计算
		if desc := strings.TrimSpace(intent.Description); desc != "" {
			texts = append(texts, desc)
			textToIntent = append(textToIntent, intent.Name)
		}
		// 所有 example 句子
		for _, example := range intent.Examples {
			if ex := strings.TrimSpace(example); ex != "" {
				texts = append(texts, ex)
				textToIntent = append(textToIntent, intent.Name)
			}
		}
	}

	if len(texts) == 0 {
		return fmt.Errorf("no text to embed: all intents have empty descriptions and examples")
	}

	// 分批调用 Embedding API，避免超出 API 批量限制
	const batchSize = 10
	var allVectors [][]float64
	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		vectors, err := x.embeddingClient.Embed(context.Background(), texts[i:end])
		if err != nil {
			return fmt.Errorf("embedding batch %d-%d failed: %v", i, end, err)
		}
		allVectors = append(allVectors, vectors...)
	}

	if len(allVectors) != len(texts) {
		return fmt.Errorf("embedding count mismatch: got %d, expected %d", len(allVectors), len(texts))
	}

	// 构建向量库
	x.intentVectors = make([]embedding.VectorEntry, 0, len(allVectors))
	for i, vec := range allVectors {
		x.intentVectors = append(x.intentVectors, embedding.VectorEntry{
			Name:   textToIntent[i],
			Vector: vec,
		})
	}

	return nil
}

// parseJSONIntents 解析 JSON 格式的意图配置文件
func parseJSONIntents(data []byte, config *intentsFileConfig) error {
	// 尝试解析为 {"intents": [...]}
	if err := json.Unmarshal(data, config); err == nil && len(config.Intents) > 0 {
		return nil
	}
	// 尝试解析为 [...]
	var intents []LocalIntent
	if err := json.Unmarshal(data, &intents); err == nil && len(intents) > 0 {
		config.Intents = intents
		return nil
	}
	return fmt.Errorf("invalid JSON format: expected {\"intents\":[...]} or [...]")
}
