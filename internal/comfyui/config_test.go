package comfyui

import "testing"

func TestDefaultAndNormalizeConfig(t *testing.T) {
	cfg := NormalizeConfig(Config{})
	if cfg.BaseURL != "http://127.0.0.1:8188" || cfg.TimeoutMS != 600000 || cfg.PollIntervalMS != 1000 {
		t.Fatalf("unexpected canonical defaults: %#v", cfg)
	}
	if got := NormalizeConfig(Config{BaseURL: "http://example:8188", TimeoutMS: 42, PollIntervalMS: 7}); got.TimeoutMS != 42 || got.PollIntervalMS != 7 {
		t.Fatalf("NormalizeConfig overwrote explicit values: %#v", got)
	}
}
