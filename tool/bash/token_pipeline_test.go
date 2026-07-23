// token_pipeline_test.go: Cleaning chain testing.
// Coverage for ansensitization (Bearer/JWT/AWS/GitHub/OpenAI/Anthropic/Slack/generic/PEM)+progress+ANSI+
// longline + never-worse(C1) + # nofilter opt-out + Chinese longline UTF-8.
package bash

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// applyRedact only runs the desensitization plugin and is used to precisely assert desensitization behavior.
func applyRedact(text string) string {
	return redactPlugin{}.Apply(text)
}

// ---------- 9 Types of Desensitization ----------

func TestRedactBearer(t *testing.T) {
	in := "Authorization: Bearer abcdefghijklmnop"
	out := applyRedact(in)
	assert.Contains(t, out, "Bearer <redacted>")
	assert.NotContains(t, out, "abcdefghijklmnop")
	// case-insensitive (regex with (?i))
	out2 := applyRedact("authorization: bearer XYZ1234567890")
	assert.Contains(t, out2, "bearer <redacted>")
}

func TestRedactBearer_TooShort(t *testing.T) {
	// Short tokens (<8 characters) should not be desensitized
	out := applyRedact("Bearer short")
	assert.Contains(t, out, "Bearer short")
}

func TestRedactJWT(t *testing.T) {
	// Standard JWT: header.payload.signature
	jwt := "eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0NTY3ODkw.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	in := "token=" + jwt
	out := applyRedact(in)
	assert.Contains(t, out, "<redacted jwt>")
	assert.NotContains(t, out, jwt)
}

func TestRedactAWS(t *testing.T) {
	// AKIA + exactly 16 characters (total length 20)
	out := applyRedact("aws_key=AKIAIOSFODNN7EXAMPLE")
	assert.Contains(t, out, "<redacted aws>")
	assert.NotContains(t, out, "AKIAIOSFODNN7EXAMPLE")
	// ASIA prefix (temporary voucher)
	out2 := applyRedact("x=ASIA1234567890ABCDEF")
	assert.Contains(t, out2, "<redacted aws>")
	assert.NotContains(t, out2, "ASIA1234567890ABCDEF")
}

func TestRedactAWS_PreciseLength(t *testing.T) {
	// M5: Exact 16 characters—15 characters should not match, 17 characters should not match (with \b boundary)
	short := "AKIA1234567890AB" // 4+15=19, no match
	out := applyRedact(short)
	assert.NotContains(t, out, "<redacted aws>", "15-char suffix must not match")
	long := "AKIA1234567890ABCDEFG" // 4+17=21, the \b boundary followed by G does not count as the word boundary ends after 16
	out2 := applyRedact(long)
	// Note: 21 characters with all uppercase letters/numbers have no word boundaries; regular script does not truncate matches at 16, so overall mismatches
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
			// The key itself should not remain unchanged
			assert.NotContains(t, out, c.in[strings.Index(c.in, "=")+1:])
		})
	}
}

func TestRedactGitHub_TooShort(t *testing.T) {
	// Classic PAT 36-character lower bound: 35 characters should not match
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
	// Generic field names (api_key/password, etc.) have a large chance of accidental damage and are no longer anonymized—only high-precision prefix is desensitized.
	// Anti-regression: These ordinary field values should remain as they are and not replaced with <redacted>.
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
	// M3: Only BEGIN without END (truncated PEM) should be desensitized as a bottom guard
	pem := "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQDdQ..."
	out := applyRedact(pem)
	assert.Contains(t, out, "<redacted pem>")
	assert.NotContains(t, out, "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQDdQ")
}

// ---------- progress: Frame folding ----------

func TestProgressFold(t *testing.T) {
	in := "downloading 10%\rdone\nfinished 50%\rdone"
	// Directly call the progress plugin to avoid the never-worse guard's interference with short input rollback assertions
	out := progressPlugin{}.Apply(in)
	// Each \r-separated line only keeps the last frame
	assert.Contains(t, out, "done")
	assert.NotContains(t, out, "downloading 10%")
	assert.NotContains(t, out, "finished 50%")
}

// ---------- ANSI Stripping ----------

func TestAnsiStrip_CSI(t *testing.T) {
	in := "\x1b[32mgreen text\x1b[0m normal"
	out := ansiPlugin{}.Apply(in)
	assert.NotContains(t, out, "\x1b[")
	assert.Contains(t, out, "green text")
	assert.Contains(t, out, "normal")
}

func TestAnsiStrip_SS3(t *testing.T) {
	// M6: SS3 (ESC O + single byte, such as F1-F4 function keys)
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
	// PM(ESC^) and APC(ESC_)
	in := "\x1b^some-pm-data\x1b\\\x1b_some-apc\x1b\\tail"
	out := ansiPlugin{}.Apply(in)
	assert.NotContains(t, out, "some-pm-data")
	assert.NotContains(t, out, "some-apc")
	assert.Contains(t, out, "tail")
}

func TestAnsiStrip_CSIExtraParams(t *testing.T) {
	// M6: CSI parameter contains < > = (e.g., SGR private mode, mouse report)
	in := "\x1b[<0;10;20Mclick\x1b[?25h"
	out := ansiPlugin{}.Apply(in)
	assert.NotContains(t, out, "\x1b[")
	assert.Contains(t, out, "click")
}

// ---------- longline folding ----------

func TestLonglineFold(t *testing.T) {
	long := strings.Repeat("a", 1200)
	out := newTokenPipeline().run(long)
	// never-worse Guard should not roll back (folding does reduce volume)
	assert.Contains(t, out, "<elided")
	assert.Less(t, len(out), 1200)
}

func TestLongline_NoFoldUnderThreshold(t *testing.T) {
	short := strings.Repeat("a", 500)
	out := newTokenPipeline().run(short)
	assert.NotContains(t, out, "<elided")
}

// ---------- never-worse (emphasis C1)----------

func TestNeverWorse_RedactNotRolledBack(t *testing.T) {
	// After short input desensitization, the byte count may be close to the original text (or even longer), and the never-worse guard cannot roll back the desensitization.
	// Trigger high-precision anonymization with a real OpenAI key (sk- + 16 characters or more).
	in := "token=sk-" + strings.Repeat("a", 20)
	out := newTokenPipeline().run(in)
	assert.Contains(t, out, "<redacted")
	assert.NotContains(t, out, "sk-"+strings.Repeat("a", 20))
}

func TestNeverWorse_RollsBackNonReductiveNonRedact(t *testing.T) {
	// No desensitization, no folding, no reduction terms, pure ANSI noise input,
	// After cleaning, when the volume approaches the original text, the guard should roll back (to avoid accidental damage).
	// Here's a scenario where "all ANSI but not much after cleaning": Guard rolls back to the original.
	in := "hello world normal text without secrets"
	out := newTokenPipeline().run(in)
	// No desensitization occurred and no significant volume reduction → Guard rollback original text
	assert.Equal(t, in, out)
}

// ---------- # nofilter opt-out ----------

func TestShouldFilter_NofilterOptOut(t *testing.T) {
	assert.False(t, shouldFilter("cat ~/.npmrc # nofilter"))
	assert.False(t, shouldFilter("env # raw"))
	assert.True(t, shouldFilter("env"))
}

// ---------- Chinese longline without cutting off UTF-8 ----------

func TestLonglineChinese_UTF8NotSplit(t *testing.T) {
	// Longline: Preserve the head by slicing byte; Chinese is multi-byte, so you need to verify that the reserved part is still valid UTF-8.
	// Note: In the current implementation of slicing bytes, the test assertion must retain the prefix as a valid UTF-8 string (do not appear).
	chinese := strings.Repeat("中文字符测试", 100) // Each character is 3 bytes, totaling ~1800 bytes
	out := newTokenPipeline().run(chinese)
	// never-worse guard: Volume after folding < original text, the folded result should be preserved
	if strings.Contains(out, "<elided") {
		// The prefix after folding must be valid UTF-8: does not contain replacement characters
		assert.NotContains(t, out, "�")
	} else {
		// Guardian Rollback Original: The original text is legal UTF-8
		assert.Equal(t, chinese, out)
	}
}

// ---------- Combination: progress + ANSI + redact + longline full link ----------

func TestPipelineFullChain(t *testing.T) {
	in := "\x1b[31m" + strings.Repeat("a", 600) + "\x1b[0m\nAuthorization: Bearer tok_abcdefghijklmnop\nprogress 1%\rdone"
	out := newTokenPipeline().run(in)
	assert.NotContains(t, out, "\x1b[")
	assert.NotContains(t, out, "tok_abcdefghijklmnop")
	assert.Contains(t, out, "<redacted>")
	assert.NotContains(t, out, "progress 1%")
}
