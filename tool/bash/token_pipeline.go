// Command output token to clean the chain.
//
// Processing sequence:
//
//	progress frame (\r redraw leaving only the last frame) → ANSI strip →
//	Key anonymization (Bearer/JWT/AWS/GitHub/OpenAI/Anthropic/Slack/Universal api_key/PEM blocks) →
//	longline (extra-long line folding).
//
// never-worse Guard: If the amount does not decrease after cleansing, roll back to the original text to avoid accidental damage and legal output;
// However, desensitization is irreversible; once desensitization occurs, replacement does not roll back.
// opt-out: Skips the entire chain when the command contains `# nofilter` or `# raw`.
package bash

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	longlineThreshold = 1000 // Lines exceeding this length trigger a fold (byte; Chinese about 333 characters)
	longlineKeep      = 240  // When folding, retain the number of characters in the header
	neverWorseMargin  = 64   // never-worse guards margin
)

// pipelinePlugin is a processing plugin in the cleaning chain.
type pipelinePlugin interface {
	Name() string
	Apply(text string) string
}

// tokenPipeline cleansing the chain. Apply each plugin in order, with never-worse guarding at the end of the chain.
type tokenPipeline struct {
	plugins []pipelinePlugin
}

// newTokenPipeline constructs the default L1 cleansing chain (progress → ansi → redact → longline).
func newTokenPipeline() *tokenPipeline {
	return &tokenPipeline{
		plugins: []pipelinePlugin{
			progressPlugin{},
			ansiPlugin{},
			redactPlugin{},
			longlinePlugin{},
		},
	}
}

// Run order application plugins. Desensitization is irreversible; skipping the never-worse guard during desensitization replacement,
// This prevents rollback and desensitization after short inputs are replaced with longer placeholders, causing longer byte counts.
func (p *tokenPipeline) run(text string) string {
	if text == "" {
		return text
	}
	out := text
	redacted := false
	for _, pl := range p.plugins {
		next := pl.Apply(out)
		if pl.Name() == "redact" && next != out {
			redacted = true
		}
		out = next
	}
	// If no desensitization occurs and the reduction after washing does not significantly decrease, roll back to the original text
	if !redacted && len(out)+neverWorseMargin >= len(text) {
		return text
	}
	return out
}

// nofilterOptOut matches the opt-out tag in shell comment format (# preceded by line header/space/; &|),
// Avoid misreading the literal "# nofilter" within the command body (such as in quotation marks).
var nofilterOptOut = regexp.MustCompile(`(?:^|[\s;&|])#\s*(?:nofilter|raw)\b`)

// shouldFilter skips the cleaning chain when the #nofilter / #raw comment is included.
func shouldFilter(command string) bool {
	return !nofilterOptOut.MatchString(command)
}

// ============================================================================
// progress: Redraw only the last frame
// ============================================================================

// progressPlugin for each row separated by \n, press \r to slice and keep only the last segment.
type progressPlugin struct{}

func (progressPlugin) Name() string { return "progress" }

func (progressPlugin) Apply(text string) string {
	if !strings.ContainsRune(text, '\r') {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		idx := strings.LastIndexByte(ln, '\r')
		if idx >= 0 {
			lines[i] = ln[idx+1:]
		}
	}
	return strings.Join(lines, "\n")
}

// ============================================================================
// ANSI Escape Stripping: CSI / OSC / DCS / SS3 sequences + backspace/ringtone control characters
// ============================================================================

type ansiPlugin struct{}

func (ansiPlugin) Name() string { return "ansi" }

// ansiEscape matches common ANSI/DEC escape sequences:
//
//	CSI (including < >=? and other parameters), OSC, DCS/SOS/PM/APC (terminate at ST), SS2/SS3, and other two/three-byte sequences
var ansiEscape = regexp.MustCompile(
	`\x1b\[[0-9;:<=>?]*[!-/]*[@-~]` + // CSI
		`|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)` + // OSC
		`|\x1b[PX^_][^\x1b]*(?:\x1b\\)` + // DCS / SOS / PM / APC
		`|\x1b[NO].` + // SS2 / SS3 + single bytes
		`|\x1b[=>]|\x1b[()*+].`) // Other two/three-byte sequences

func (ansiPlugin) Apply(text string) string {
	if !strings.ContainsRune(text, 0x1b) {
		return stripControlKeepNL(text)
	}
	out := ansiEscape.ReplaceAllString(text, "")
	return stripControlKeepNL(out)
}

// stripControlKeepNL removes backspace/ring/vertical tab/page break, keeps \n \r \t.
func stripControlKeepNL(s string) string {
	if !strings.ContainsAny(s, "\b\a\v\f") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\b', '\a', '\v', '\f':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ============================================================================
// Key Anonymization: Replace known key patterns with <redacted>to avoid leaking into the agent's context
// ============================================================================

type redactPlugin struct{}

func (redactPlugin) Name() string { return "redact" }

var (
	redactBearer = regexp.MustCompile(`(?i)\b(Bearer|Token)\s+[A-Za-z0-9\-_\.=]{8,}`)
	redactJWT    = regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`)
	// AWS access key ID: AKIA/ASIA + exactly 16 uppercase letters/numbers
	redactAWS = regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)
	// GitHub token: Classic PAT(gh[pousr]_) + fine-grained(github_pat_) + ghg_/ghd_
	redactGitHub    = regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,251}\b|github_pat_[A-Za-z0-9_]{82,}|\bgh[gd]_[A-Za-z0-9]{20,}\b`)
	redactOpenAI    = regexp.MustCompile(`\bsk-[A-Za-z0-9]{16,}\b`)
	redactAnthropic = regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_\-]{16,}\b`)
	redactSlack     = regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9\-]{10,}\b`)
)

// redactPEMBlocks fully desensitized BEGIN...END PEM block
var redactPEMBlocks = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]+-----.*?-----END [A-Z ]+-----`)

// redactPEMBeginOnly Guarantee: Only BEGIN No END (truncated/damaged PEM),
// From BEGIN desensitization to the first blank line or end of the text (PEM body is base64 without blank lines)
var redactPEMBeginOnly = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]+-----.*?(?:\n\s*\n|$)`)

func (redactPlugin) Apply(text string) string {
	out := text
	out = redactPEMBlocks.ReplaceAllString(out, "<redacted pem>")
	out = redactPEMBeginOnly.ReplaceAllString(out, "<redacted pem>")
	out = redactBearer.ReplaceAllString(out, "$1 <redacted>")
	out = redactJWT.ReplaceAllString(out, "<redacted jwt>")
	out = redactAWS.ReplaceAllString(out, "<redacted aws>")
	out = redactGitHub.ReplaceAllString(out, "<redacted github token>")
	out = redactOpenAI.ReplaceAllString(out, "<redacted openai key>")
	out = redactAnthropic.ReplaceAllString(out, "<redacted anthropic key>")
	out = redactSlack.ReplaceAllString(out, "<redacted slack token>")
	return out
}

// ============================================================================
// Longline fold: Extra-long line preserves the head + omits marks
// ============================================================================

// longlinePlugin reserves the header longlineKeep character for lines exceeding the longlineThreshold character.
type longlinePlugin struct{}

func (longlinePlugin) Name() string { return "longline" }

func (longlinePlugin) Apply(text string) string {
	if !strings.ContainsRune(text, '\n') {
		// Special case of single-line without line breaks: Check the entire paragraph
		if len(text) > longlineThreshold {
			return foldLine(text)
		}
		return text
	}
	lines := strings.Split(text, "\n")
	changed := false
	for i, ln := range lines {
		if len(ln) > longlineThreshold {
			lines[i] = foldLine(ln)
			changed = true
		}
	}
	if !changed {
		return text
	}
	return strings.Join(lines, "\n")
}

// foldLine folded single line: keep the longlineKeep character at the header, and mark the rest with <elided N chars>.
func foldLine(ln string) string {
	if len(ln) <= longlineThreshold {
		return ln
	}
	keep := longlineKeep
	if keep > len(ln) {
		keep = len(ln)
	}
	return fmt.Sprintf("%s <elided %d chars>", ln[:keep], len(ln)-keep)
}
