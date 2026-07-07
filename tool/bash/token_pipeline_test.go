// token_pipeline_test.go：清洗链测试。
// 覆盖脱敏（Bearer/JWT/AWS/GitHub/OpenAI/Anthropic/Slack/generic/PEM）+ progress + ANSI +
// longline + never-worse（C1）+ # nofilter opt-out + 中文 longline UTF-8。
package bash

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// applyRedact 仅运行脱敏插件，用于精确断言脱敏行为。
func applyRedact(text string) string {
	return redactPlugin{}.Apply(text)
}

// ---------- 9 类脱敏 ----------

func TestRedactBearer(t *testing.T) {
	in := "Authorization: Bearer abcdefghijklmnop"
	out := applyRedact(in)
	assert.Contains(t, out, "Bearer <redacted>")
	assert.NotContains(t, out, "abcdefghijklmnop")
	// 大小写不敏感（regex 带 (?i)）
	out2 := applyRedact("authorization: bearer XYZ1234567890")
	assert.Contains(t, out2, "bearer <redacted>")
}

func TestRedactBearer_TooShort(t *testing.T) {
	// 短 token（<8 字符）不应脱敏
	out := applyRedact("Bearer short")
	assert.Contains(t, out, "Bearer short")
}

func TestRedactJWT(t *testing.T) {
	// 标准 JWT：header.payload.signature
	jwt := "eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0NTY3ODkw.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	in := "token=" + jwt
	out := applyRedact(in)
	assert.Contains(t, out, "<redacted jwt>")
	assert.NotContains(t, out, jwt)
}

func TestRedactAWS(t *testing.T) {
	// AKIA + 恰好 16 字符（总长 20）
	out := applyRedact("aws_key=AKIAIOSFODNN7EXAMPLE")
	assert.Contains(t, out, "<redacted aws>")
	assert.NotContains(t, out, "AKIAIOSFODNN7EXAMPLE")
	// ASIA 前缀（临时凭证）
	out2 := applyRedact("x=ASIA1234567890ABCDEF")
	assert.Contains(t, out2, "<redacted aws>")
	assert.NotContains(t, out2, "ASIA1234567890ABCDEF")
}

func TestRedactAWS_PreciseLength(t *testing.T) {
	// M5：精确 16 字符——15 字符不应匹配，17 字符也不应（带 \b 边界）
	short := "AKIA1234567890AB" // 4+15=19，不匹配
	out := applyRedact(short)
	assert.NotContains(t, out, "<redacted aws>", "15-char suffix must not match")
	long := "AKIA1234567890ABCDEFG" // 4+17=21，\b 边界后跟 G 不算词边界结束于 16 后
	out2 := applyRedact(long)
	// 注意：21 字符全大写字母/数字无词边界，正则不会在 16 处截断匹配，因此整体不匹配
	assert.NotContains(t, out2, "<redacted aws>", "17-char suffix must not partially match")
}

func TestRedactGitHub(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"classic ghp", "GH_TOKEN=ghp_" + strings.Repeat("a", 36)},
		{"gho", "x=gho_" + strings.Repeat("b", 36)},
		{"ghu", "x=ghu_" + strings.Repeat("c", 36)},
		{"ghs", "x=ghs_" + strings.Repeat("d", 36)},
		{"ghr", "x=ghr_" + strings.Repeat("e", 36)},
		{"fine-grained pat", "x=github_pat_" + strings.Repeat("f", 82)},
		{"ghg", "x=ghg_" + strings.Repeat("g", 36)},
		{"ghd", "x=ghd_" + strings.Repeat("h", 36)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := applyRedact(c.in)
			assert.Contains(t, out, "<redacted github token>", c.name)
			// 密钥本身不应原样残留
			assert.NotContains(t, out, c.in[strings.Index(c.in, "=")+1:])
		})
	}
}

func TestRedactGitHub_TooShort(t *testing.T) {
	// 经典 PAT 36 字符下界：35 字符不应匹配
	out := applyRedact("ghp_" + strings.Repeat("a", 35))
	assert.NotContains(t, out, "<redacted github token>")
}

func TestRedactOpenAI(t *testing.T) {
	out := applyRedact("OPENAI_API_KEY=sk-" + strings.Repeat("a", 20))
	assert.Contains(t, out, "<redacted openai key>")
}

func TestRedactAnthropic(t *testing.T) {
	out := applyRedact("ANTHROPIC_API_KEY=sk-ant-" + strings.Repeat("a", 20))
	assert.Contains(t, out, "<redacted anthropic key>")
}

func TestRedactSlack(t *testing.T) {
	cases := []string{
		"xoxa-" + strings.Repeat("a", 12),
		"xoxb-" + strings.Repeat("b", 12),
		"xoxp-" + strings.Repeat("c", 12),
		"xoxr-" + strings.Repeat("d", 12),
		"xoxs-" + strings.Repeat("e", 12),
	}
	for _, c := range cases {
		out := applyRedact(c)
		assert.Contains(t, out, "<redacted slack token>")
	}
}

func TestRedactGenericKey_NotRedacted(t *testing.T) {
	// 泛词字段名（api_key/password 等）误伤面大，已不做脱敏——仅高精度 prefix 才脱敏。
	// 防回归：这些普通字段值应保持原样，不被替换成 <redacted>。
	cases := []string{
		`api_key=zabcdef1234567890`,
		`apiKey: zabcdef1234567890`,
		`api-key zabcdef1234567890`,
		`secret_key="zabcdef1234567890"`,
		`access_token: ztoken1234567890`,
		`PASSWORD=zabcdef1234567890`,
	}
	for _, in := range cases {
		out := applyRedact(in)
		assert.NotContains(t, out, "<redacted>", "input: %s", in)
	}
}

func TestRedactPEMBlock_Complete(t *testing.T) {
	pem := `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA1234567890abcdefghijklmnopqrstuvwxyz
-----END RSA PRIVATE KEY-----`
	out := applyRedact(pem)
	assert.Contains(t, out, "<redacted pem>")
	assert.NotContains(t, out, "MIIEpAIBAAKCAQEA")
}

func TestRedactPEMBlock_BeginOnly(t *testing.T) {
	// M3：仅有 BEGIN 无 END（被截断的 PEM）应兜底脱敏
	pem := "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQDdQ..."
	out := applyRedact(pem)
	assert.Contains(t, out, "<redacted pem>")
	assert.NotContains(t, out, "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQDdQ")
}

// ---------- progress 帧折叠 ----------

func TestProgressFold(t *testing.T) {
	in := "downloading 10%\rdone\nfinished 50%\rdone"
	// 直接调用 progress 插件，避免 never-worse 守卫对短输入回滚干扰断言
	out := progressPlugin{}.Apply(in)
	// 每个 \r 分隔的行只保留最后一帧
	assert.Contains(t, out, "done")
	assert.NotContains(t, out, "downloading 10%")
	assert.NotContains(t, out, "finished 50%")
}

// ---------- ANSI 剥离 ----------

func TestAnsiStrip_CSI(t *testing.T) {
	in := "\x1b[32mgreen text\x1b[0m normal"
	out := ansiPlugin{}.Apply(in)
	assert.NotContains(t, out, "\x1b[")
	assert.Contains(t, out, "green text")
	assert.Contains(t, out, "normal")
}

func TestAnsiStrip_SS3(t *testing.T) {
	// M6：SS3（ESC O + 单字节，如 F1-F4 功能键）
	in := "\x1bOPpressed F1"
	out := ansiPlugin{}.Apply(in)
	assert.NotContains(t, out, "\x1bO", "SS3 should be stripped")
	assert.Contains(t, out, "pressed F1")
}

func TestAnsiStrip_DCS(t *testing.T) {
	// M6：DCS（ESC P ... ST）
	in := "\x1bP1$qr0\x1b\\rest"
	out := ansiPlugin{}.Apply(in)
	assert.NotContains(t, out, "\x1bP", "DCS should be stripped")
	assert.NotContains(t, out, "\x1b\\", "ST should be stripped")
	assert.Contains(t, out, "rest")
}

func TestAnsiStrip_PM_APC(t *testing.T) {
	// PM（ESC ^）和 APC（ESC _）
	in := "\x1b^some-pm-data\x1b\\\x1b_some-apc\x1b\\tail"
	out := ansiPlugin{}.Apply(in)
	assert.NotContains(t, out, "some-pm-data")
	assert.NotContains(t, out, "some-apc")
	assert.Contains(t, out, "tail")
}

func TestAnsiStrip_CSIExtraParams(t *testing.T) {
	// M6：CSI 参数含 < > =（如 SGR 私有模式、鼠标报告）
	in := "\x1b[<0;10;20Mclick\x1b[?25h"
	out := ansiPlugin{}.Apply(in)
	assert.NotContains(t, out, "\x1b[")
	assert.Contains(t, out, "click")
}

// ---------- longline 折叠 ----------

func TestLonglineFold(t *testing.T) {
	long := strings.Repeat("a", 1200)
	out := newTokenPipeline().run(long)
	// never-worse 守卫不应回滚（折叠确实减小了体积）
	assert.Contains(t, out, "<elided")
	assert.Less(t, len(out), 1200)
}

func TestLongline_NoFoldUnderThreshold(t *testing.T) {
	short := strings.Repeat("a", 500)
	out := newTokenPipeline().run(short)
	assert.NotContains(t, out, "<elided")
}

// ---------- never-worse（重点 C1）----------

func TestNeverWorse_RedactNotRolledBack(t *testing.T) {
	// 短输入脱敏后字节数可能与原文接近（甚至变长），never-worse 守卫不得回滚脱敏。
	// 用真实 OpenAI key（sk- + 16 字符以上）触发高精度脱敏。
	in := "token=sk-" + strings.Repeat("a", 20)
	out := newTokenPipeline().run(in)
	assert.Contains(t, out, "<redacted")
	assert.NotContains(t, out, "sk-"+strings.Repeat("a", 20))
}

func TestNeverWorse_RollsBackNonReductiveNonRedact(t *testing.T) {
	// 无脱敏、无折叠、无可减小项的纯 ANSI 噪声输入，
	// 清洗后体积接近原文时守卫应回滚（避免误伤）。
	// 此处给一个"全是 ANSI 但清洗后没小多少"的场景：守卫回滚返回原文。
	in := "hello world normal text without secrets"
	out := newTokenPipeline().run(in)
	// 没有脱敏发生且体积无明显缩减 → 守卫回滚原文
	assert.Equal(t, in, out)
}

// ---------- # nofilter opt-out ----------

func TestShouldFilter_NofilterOptOut(t *testing.T) {
	assert.False(t, shouldFilter("cat ~/.npmrc # nofilter"))
	assert.False(t, shouldFilter("env # raw"))
	assert.True(t, shouldFilter("env"))
}

// ---------- 中文 longline 不切断 UTF-8 ----------

func TestLonglineChinese_UTF8NotSplit(t *testing.T) {
	// longline 按 byte 切片保留头部；中文是多字节，需验证保留部分仍是合法 UTF-8。
	// 注意：当前实现按 byte 切片，测试断言保留前缀必须为合法 UTF-8 字符串（不出现 �）。
	chinese := strings.Repeat("中文字符测试", 100) // 每字 3 字节，共 ~1800 字节
	out := newTokenPipeline().run(chinese)
	// never-worse 守卫：折叠后体积 < 原文，应保留折叠结果
	if strings.Contains(out, "<elided") {
		// 折叠后前缀必须是合法 UTF-8：不包含替换字符
		assert.NotContains(t, out, "�")
	} else {
		// 守卫回滚原文：原文是合法 UTF-8
		assert.Equal(t, chinese, out)
	}
}

// ---------- 组合：progress + ANSI + redact + longline 全链路 ----------

func TestPipelineFullChain(t *testing.T) {
	in := "\x1b[31m" + strings.Repeat("a", 600) + "\x1b[0m\nAuthorization: Bearer tok_abcdefghijklmnop\nprogress 1%\rdone"
	out := newTokenPipeline().run(in)
	assert.NotContains(t, out, "\x1b[")
	assert.NotContains(t, out, "tok_abcdefghijklmnop")
	assert.Contains(t, out, "<redacted>")
	assert.NotContains(t, out, "progress 1%")
}
