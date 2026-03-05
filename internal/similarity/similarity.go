// Package similarity provides TF-based cosine similarity for news deduplication.
// No external dependencies — pure Go, runs on CPU.
package similarity

import (
	"math"
	"strings"
	"unicode"
)

// Threshold is the default minimum cosine similarity to treat articles as duplicates.
const Threshold = 0.55

// Cosine returns cosine similarity (0..1) between two texts
// using normalized term-frequency vectors.
func Cosine(a, b string) float64 {
	va := vectorize(a)
	vb := vectorize(b)
	return cosineSim(va, vb)
}

func cosineSim(a, b map[string]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var dot, magA, magB float64
	for k, va := range a {
		dot += va * b[k]
		magA += va * va
	}
	for _, vb := range b {
		magB += vb * vb
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (math.Sqrt(magA) * math.Sqrt(magB))
}

func vectorize(text string) map[string]float64 {
	tokens := tokenize(text)
	freq := make(map[string]float64, len(tokens))
	for _, t := range tokens {
		freq[t]++
	}
	n := float64(len(tokens))
	if n == 0 {
		return freq
	}
	for k := range freq {
		freq[k] /= n
	}
	return freq
}

func tokenize(text string) []string {
	text = strings.ToLower(text)
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	result := make([]string, 0, len(fields))
	for _, f := range fields {
		if len([]rune(f)) >= 3 && !stopwords[f] {
			result = append(result, f)
		}
	}
	return result
}

var stopwords = map[string]bool{
	// Russian
	"это": true, "как": true, "так": true, "что": true, "его": true, "все": true,
	"она": true, "они": true, "мы": true, "вы": true, "он": true, "для": true,
	"при": true, "без": true, "из": true, "или": true, "уже": true, "был": true,
	"была": true, "были": true, "будет": true, "будут": true, "по": true,
	"на": true, "не": true, "но": true, "и": true, "в": true, "с": true,
	"к": true, "о": true, "об": true, "от": true, "до": true, "за": true,
	"под": true, "над": true, "про": true, "через": true, "после": true,
	"перед": true, "между": true, "же": true, "бы": true, "ли": true,
	"да": true, "нет": true, "то": true, "те": true, "тот": true,
	"эта": true, "эти": true, "этот": true, "такой": true, "такая": true,
	"такие": true, "один": true, "два": true, "три": true, "год": true,
	"лет": true, "раз": true, "может": true, "нужно": true, "надо": true,
	"когда": true, "если": true, "только": true, "тоже": true, "также": true,
	"ещё": true, "еще": true, "даже": true, "уж": true, "вот": true,
	"вся": true, "весь": true, "свой": true, "своя": true, "своё": true,
	"свои": true, "стал": true, "стала": true, "стали": true, "стать": true,
	"быть": true, "есть": true, "него": true, "неё": true, "них": true,
	"ним": true, "ней": true, "ему": true, "ей": true, "им": true,
	"тем": true, "том": true, "который": true, "которая": true,
	"которые": true, "которого": true, "которой": true, "которых": true,
	// English
	"the": true, "and": true, "for": true, "are": true, "but": true,
	"not": true, "you": true, "all": true, "can": true, "had": true,
	"her": true, "was": true, "one": true, "our": true, "out": true,
	"day": true, "get": true, "has": true, "him": true, "his": true,
	"how": true, "its": true, "may": true, "now": true, "say": true,
	"she": true, "too": true, "use": true, "way": true, "who": true,
	"did": true, "from": true, "have": true, "been": true, "more": true,
	"will": true, "with": true, "this": true, "that": true, "they": true,
	"what": true, "when": true, "your": true, "said": true, "each": true,
	"which": true, "their": true, "time": true, "about": true, "would": true,
	"there": true, "could": true, "into": true, "then": true, "than": true,
	"other": true, "also": true, "back": true, "after": true, "first": true,
	"well": true, "some": true, "those": true, "where": true,
}
