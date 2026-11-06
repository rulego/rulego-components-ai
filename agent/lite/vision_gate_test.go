package lite

import "testing"

// 门控三层语义：显式配置 > 能力注册表/内置模式表 > 未注册模型保守默认开（宁可发图不静默丢图）。
// 未注册的纯文本模型被默认开图后打 400，用 visionModel:false 止血。
func TestVisionEnabled(t *testing.T) {
	trueVal, falseVal := true, false
	cases := []struct {
		name     string
		override *bool
		model    string
		want     bool
	}{
		{"强制开", &trueVal, "any-text-model", true},
		{"强制关", &falseVal, "glm-5.3-flash", false},
		{"自动-未注册模型保守默认开", nil, "zz-custom-9", true},
	}
	for _, c := range cases {
		if got := visionEnabled(c.override, c.model); got != c.want {
			t.Fatalf("%s: 期望 %v，得到 %v", c.name, c.want, got)
		}
	}
}
