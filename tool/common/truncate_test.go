package common

import (
	"strings"
	"testing"
)

func TestTruncate_NoTruncation(t *testing.T) {
	r := Truncate("short content", TruncateOptions{})
	if r.Truncated {
		t.Fatalf("expected no truncation, got truncated: %+v", r)
	}
	if r.Content != "short content" {
		t.Fatalf("expected original, got %q", r.Content)
	}
}

func TestTruncate_HeadOnly(t *testing.T) {
	// No error keywords→ pure head
	text := strings.Repeat("normal line\n", 100)
	maxLines := 10
	r := Truncate(text, TruncateOptions{MaxLines: &maxLines, Direction: TruncHead})
	if !r.Truncated {
		t.Fatal("expected truncated")
	}
	if !strings.Contains(r.Content, "lines omitted") {
		t.Fatalf("expected omitted hint, got: %s", r.Content)
	}
	if strings.Contains(r.Content, "head and tail") {
		t.Fatal("should be head only, not head+tail")
	}
}

func TestTruncate_HeadTailOnError(t *testing.T) {
	// Tail contains an error keyword → By default, HeadTail should retain head+tail
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString("normal line\n")
	}
	sb.WriteString("ERROR: panic occurred")
	text := sb.String()
	maxLines := 20
	r := Truncate(text, TruncateOptions{MaxLines: &maxLines}) // Default TruncHeadTail
	if !r.Truncated {
		t.Fatal("expected truncated")
	}
	if !strings.Contains(r.Content, "head and tail") {
		t.Fatalf("expected head+tail hint, got: %s", r.Content)
	}
	if !strings.Contains(r.Content, "panic") {
		t.Fatal("tail should contain the error context")
	}
}

func TestTruncate_HeadTailNoErrorDegradesToHead(t *testing.T) {
	// Default is HeadTail but no errors at the tail → downgrade to pure head
	text := strings.Repeat("normal line\n", 100)
	maxLines := 10
	r := Truncate(text, TruncateOptions{MaxLines: &maxLines}) // Default is HeadTail
	if strings.Contains(r.Content, "head and tail") {
		t.Fatalf("should degrade to head only when no error in tail: %s", r.Content)
	}
	if !strings.Contains(r.Content, "lines omitted") {
		t.Fatal("expected omitted hint")
	}
}

func TestTruncate_TailDirection(t *testing.T) {
	text := strings.Repeat("normal line\n", 100)
	maxLines := 5
	r := Truncate(text, TruncateOptions{MaxLines: &maxLines, Direction: TruncTail})
	if !strings.Contains(r.Content, "showing tail") {
		t.Fatalf("expected tail hint: %s", r.Content)
	}
}

func TestTruncate_ByteLimit(t *testing.T) {
	// Single-line hyperbyte limit
	text := strings.Repeat("a", 10000)
	maxBytes := 100
	r := Truncate(text, TruncateOptions{MaxBytes: &maxBytes, Direction: TruncHead})
	if !r.Truncated {
		t.Fatal("expected truncated by byte limit")
	}
	if !strings.Contains(r.Content, "byte-limited") {
		t.Fatalf("expected byte-limited marker: %s", r.Content)
	}
}

func TestTruncate_CustomLimits(t *testing.T) {
	text := strings.Repeat("x\n", 49) + "x" // 50 lines (no blank lines at the end, split to get 50 lines)
	ml, mb := 5, 10000
	r := Truncate(text, TruncateOptions{MaxLines: &ml, MaxBytes: &mb, Direction: TruncHead})
	if !r.Truncated {
		t.Fatal("expected truncated")
	}
	// 50 - 5 = 45 rows omitted
	if !strings.Contains(r.Content, "45 lines omitted") {
		t.Fatalf("expected 45 lines omitted, got: %s", r.Content)
	}
}
