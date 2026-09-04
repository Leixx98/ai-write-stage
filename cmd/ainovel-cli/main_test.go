package main

import "testing"

func TestParseCLIOptionsDefaultsToWeb(t *testing.T) {
	opts, args, err := parseCLIOptions(nil)
	if err != nil {
		t.Fatalf("parseCLIOptions: %v", err)
	}
	if opts.Headless || len(args) != 0 {
		t.Fatalf("default options = %#v, args = %#v", opts, args)
	}
}

func TestParseCLIOptionsWebIsCompatibilityNoOp(t *testing.T) {
	opts, args, err := parseCLIOptions([]string{"--web", "--listen", "127.0.0.1:9000"})
	if err != nil {
		t.Fatalf("parseCLIOptions: %v", err)
	}
	if opts.Headless || opts.Listen != "127.0.0.1:9000" || len(args) != 0 {
		t.Fatalf("options = %#v, args = %#v", opts, args)
	}
}

func TestParseCLIOptionsHeadlessStillWorksWithLegacyWebFlag(t *testing.T) {
	opts, args, err := parseCLIOptions([]string{"--web", "--headless", "--prompt", "write"})
	if err != nil {
		t.Fatalf("parseCLIOptions: %v", err)
	}
	if !opts.Headless || opts.Prompt != "write" || len(args) != 0 {
		t.Fatalf("options = %#v, args = %#v", opts, args)
	}
}

func TestParseCLIOptionsHeadlessPromptFile(t *testing.T) {
	opts, args, err := parseCLIOptions([]string{"--headless", "--prompt-file", "request.txt"})
	if err != nil {
		t.Fatalf("parseCLIOptions: %v", err)
	}
	if !opts.Headless || opts.PromptFile != "request.txt" || len(args) != 0 {
		t.Fatalf("options = %#v, args = %#v", opts, args)
	}
}

func TestParseCLIOptionsWorkspace(t *testing.T) {
	opts, args, err := parseCLIOptions([]string{"--workspace", "边城", "--listen", "127.0.0.1:9000"})
	if err != nil {
		t.Fatalf("parseCLIOptions: %v", err)
	}
	if opts.Workspace != "边城" || opts.Listen != "127.0.0.1:9000" || len(args) != 0 {
		t.Fatalf("options = %#v, args = %#v", opts, args)
	}
}
