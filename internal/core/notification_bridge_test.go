package core

import (
	"encoding/json"
	"strings"
	"testing"

	"resolvarr/internal/core/agents"
)

// TestValidateAgentFunctions locks the per-agent Functions whitelist
// contract: empty list = "all functions" (valid), every non-empty
// entry must be a known WebhookFunction constant, no duplicates, no
// blank entries. Mirrors the rejection-reason phrasing the per-rule
// NotifyAgents validator uses (matches existing UX vocabulary).
func TestValidateAgentFunctions(t *testing.T) {
	cases := []struct {
		name    string
		funcs   []string
		wantSub string
	}{
		{"empty list = all functions", nil, ""},
		{"empty slice (not nil) also valid", []string{}, ""},
		{"single known function", []string{"tagAudio"}, ""},
		{"all webhook function constants accepted", []string{
			"tagReleaseGroups", "discover", "tagAudio", "tagVideo",
			"tagDvDetail", "recover", "syncToSecondary", "grabRename",
			"qbitSeTag", "qbitCategoryFix",
		}, ""},
		{"unknown function rejected", []string{"tagAudio", "noSuchFunction"}, "unknown function: noSuchFunction"},
		{"empty entry rejected", []string{"tagAudio", ""}, "empty entry"},
		{"whitespace-only entry rejected", []string{"  "}, "empty entry"},
		{"duplicate entry rejected", []string{"tagAudio", "tagAudio"}, "duplicate entry: tagAudio"},
		// fileDeleteClean is intentionally NOT in allWebhookFunctions
		// (retired in C7/C8 of the M-webhook delete-semantics refactor —
		// per-bucket StripOnFileDelete + AutoStripTagRgOnDelete replace
		// it). The constant exists for legacy-rule migration but no
		// new agent should subscribe to it. Lock the rejection.
		{"retired fileDeleteClean rejected", []string{"fileDeleteClean"}, "unknown function: fileDeleteClean"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAgentFunctions(tc.funcs)
			if tc.wantSub == "" {
				if err != nil {
					t.Errorf("happy case rejected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q missing %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestValidateNotificationAgentFunctions verifies the full validation
// chain: agents-package Validate runs first (provider-specific
// credential checks), then the function-whitelist check piggybacks.
// Confirms both validators surface in one call.
func TestValidateNotificationAgentFunctions(t *testing.T) {
	validAgent := func() NotificationAgent {
		return NotificationAgent{
			Name:    "Discord",
			Type:    "discord",
			Enabled: true,
			Config:  agents.Config{DiscordWebhook: "https://discord.com/api/webhooks/1/aaa"},
		}
	}

	t.Run("valid agent with no Functions accepted", func(t *testing.T) {
		if err := ValidateNotificationAgent(validAgent()); err != nil {
			t.Errorf("valid agent rejected: %v", err)
		}
	})

	t.Run("valid agent with Functions whitelist accepted", func(t *testing.T) {
		a := validAgent()
		a.Functions = []string{"tagAudio", "tagVideo", "grabRename"}
		if err := ValidateNotificationAgent(a); err != nil {
			t.Errorf("agent with Functions rejected: %v", err)
		}
	})

	t.Run("agent with unknown function rejected", func(t *testing.T) {
		a := validAgent()
		a.Functions = []string{"tagAudio", "ghostFunction"}
		err := ValidateNotificationAgent(a)
		if err == nil {
			t.Fatal("expected error for unknown function")
		}
		if !strings.Contains(err.Error(), "ghostFunction") {
			t.Errorf("error should name the bad function: %v", err)
		}
	})

	t.Run("provider-validator runs first", func(t *testing.T) {
		// Empty Discord webhook → provider rejects BEFORE the function
		// validator even runs. Pin the failure mode (provider error
		// surfaces, not "Functions invalid").
		a := validAgent()
		a.Config.DiscordWebhook = ""
		a.Functions = []string{"ghostFunction"} // also invalid
		err := ValidateNotificationAgent(a)
		if err == nil {
			t.Fatal("expected error")
		}
		// Discord provider's error wins ("discord webhook is required");
		// the function validator never gets a chance.
		if strings.Contains(err.Error(), "ghostFunction") {
			t.Errorf("function validator should not have run; provider error should win. Got: %v", err)
		}
	})
}

// TestAgentFunctionsJSONRoundTrip locks the on-disk shape: Functions
// is `json:"functions,omitempty"` so a nil/empty slice serialises to
// nothing on disk, an empty agent (legacy) loads with Functions=nil
// (= "all" per the doc-contract). Critical for backward compat —
// existing agents on disk should not gain or lose semantics on
// load/save cycles.
func TestAgentFunctionsJSONRoundTrip(t *testing.T) {
	// Legacy agent on disk (no functions key): Functions field must
	// deserialise to nil (= "all" per contract).
	var legacy NotificationAgent
	body := []byte(`{"id":"a1","name":"Discord","type":"discord","enabled":true}`)
	if err := json.Unmarshal(body, &legacy); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if legacy.Functions != nil {
		t.Errorf("legacy agent Functions = %v, want nil", legacy.Functions)
	}

	// Empty Functions slice should re-marshal with the key omitted
	// (omitempty). On-disk shape stays clean.
	a := NotificationAgent{ID: "a1", Name: "n", Type: "discord", Enabled: true}
	out, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), `"functions"`) {
		t.Errorf("nil Functions serialised key; omitempty broken. Body: %s", out)
	}

	// Populated Functions round-trips.
	a.Functions = []string{"tagAudio", "tagVideo"}
	out, err = json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"functions":["tagAudio","tagVideo"]`) {
		t.Errorf("populated Functions did not round-trip. Body: %s", out)
	}
	var back NotificationAgent
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("decode roundtrip: %v", err)
	}
	if len(back.Functions) != 2 || back.Functions[0] != "tagAudio" || back.Functions[1] != "tagVideo" {
		t.Errorf("roundtrip lost Functions: %v", back.Functions)
	}
}
