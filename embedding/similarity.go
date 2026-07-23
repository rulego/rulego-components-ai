package embedding

import "math"

// CosineSimilarity calculates the cosine similarity of two vectors and returns a value between [-1, 1].
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

// VectorEntry links the name to the vector
type VectorEntry struct {
	Name   string
	Vector []float64
}

// BestMatch finds entries in the vector library with the highest similarity to the target vector cosine.
// Returns the name and similarity score of the matching entry. Empty warehouse returns ("", 0).
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

// MatchResult
type MatchResult struct {
	Name   string  // Best match name
	Score  float64 // Optimal match score
	Gap    float64 // The score difference between the best and second place (if there is only one entry, the score itself is used)
	Runner string  // Second place name
}

// BestMatchWithGap finds the best match while calculating the score gap with the runner-up.
// Used to determine whether the match has sufficient distinctiveness: a gap too small indicates the input is unclear.
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

	// Find the top 2
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
