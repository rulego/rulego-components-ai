package embedding

import "math"

// CosineSimilarity 计算两个向量的余弦相似度，返回 [-1, 1] 之间的值
func CosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// VectorEntry 向量条目，将名称与向量关联
type VectorEntry struct {
	Name   string
	Vector []float64
}

// BestMatch 在向量库中找到与目标向量余弦相似度最高的条目。
// 返回匹配条目的名称和相似度分数。空库返回 ("", 0)。
func BestMatch(target []float64, entries []VectorEntry) (string, float64) {
	if len(entries) == 0 {
		return "", 0
	}
	bestName := entries[0].Name
	bestScore := CosineSimilarity(target, entries[0].Vector)
	for i := 1; i < len(entries); i++ {
		score := CosineSimilarity(target, entries[i].Vector)
		if score > bestScore {
			bestScore = score
			bestName = entries[i].Name
		}
	}
	return bestName, bestScore
}

// MatchResult 匹配结果
type MatchResult struct {
	Name   string  // 最佳匹配名称
	Score  float64 // 最佳匹配分数
	Gap    float64 // 最佳与第二名的分数差距（只有1个条目时为 Score 本身）
	Runner string // 第二名名称
}

// BestMatchWithGap 找到最佳匹配，同时计算与第二名的分数差距。
// 用于判断匹配是否具有足够的区分度：gap 太小说明输入模糊不清。
func BestMatchWithGap(target []float64, entries []VectorEntry) MatchResult {
	if len(entries) == 0 {
		return MatchResult{}
	}
	if len(entries) == 1 {
		return MatchResult{
			Name:  entries[0].Name,
			Score: CosineSimilarity(target, entries[0].Vector),
			Gap:   CosineSimilarity(target, entries[0].Vector),
		}
	}

	// 找 top-2
	first, second := 0, 1
	scoreFirst := CosineSimilarity(target, entries[0].Vector)
	scoreSecond := CosineSimilarity(target, entries[1].Vector)
	if scoreSecond > scoreFirst {
		first, second = second, first
		scoreFirst, scoreSecond = scoreSecond, scoreFirst
	}

	for i := 2; i < len(entries); i++ {
		s := CosineSimilarity(target, entries[i].Vector)
		if s > scoreFirst {
			scoreSecond = scoreFirst
			second = first
			scoreFirst = s
			first = i
		} else if s > scoreSecond {
			scoreSecond = s
			second = i
		}
	}

	return MatchResult{
		Name:   entries[first].Name,
		Score:  scoreFirst,
		Gap:    scoreFirst - scoreSecond,
		Runner: entries[second].Name,
	}
}
