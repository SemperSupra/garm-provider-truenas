package truenasstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	truenas "github.com/deevus/truenas-go"
	tnclient "github.com/deevus/truenas-go/client"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

func TestCallbackHostGatewayDisabledPreservesCompose(t *testing.T) {
	spec := fixedSpec()
	base, err := composeConfig(spec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := applyCallbackHostGateway(base, spec, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != base {
		t.Fatal("disabled callback host-gateway policy must preserve prior deterministic Compose bytes")
	}
}

func TestCallbackHostGatewayEnabledDerivesOneMapping(t *testing.T) {
	spec := fixedSpec()
	base, err := composeConfig(spec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := applyCallbackHostGateway(base, spec, true)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatal(err)
	}
	runner := doc["services"].(map[string]any)["runner"].(map[string]any)
	extraHosts := runner["extra_hosts"].([]any)
	if len(extraHosts) != 1 || extraHosts[0] != "garm.example.invalid:host-gateway" {
		t.Fatalf("unexpected derived mapping: %#v", extraHosts)
	}
	labels := runner["labels"].(map[string]any)
	if labels[labelCallbackHostGateway] != "true" {
		t.Fatalf("missing host-gateway ownership label: %#v", labels)
	}
}

func TestCallbackHostGatewayRejectsAmbiguousEndpoints(t *testing.T) {
	cases := []struct {
		name     string
		callback string
		metadata string
	}{
		{"host mismatch", "https://garm-a.example.invalid/callback", "https://garm-b.example.invalid/metadata"},
		{"callback not https", "http://garm.example.invalid/callback", "https://garm.example.invalid/metadata"},
		{"metadata not https", "https://garm.example.invalid/callback", "http://garm.example.invalid/metadata"},
		{"ipv4 literal", "https://192.0.2.10/callback", "https://192.0.2.10/metadata"},
		{"ipv6 literal", "https://[2001:db8::10]/callback", "https://[2001:db8::10]/metadata"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := fixedSpec()
			spec.CallbackURL = tc.callback
			spec.MetadataURL = tc.metadata
			base, err := composeConfig(spec)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := applyCallbackHostGateway(base, spec, true); !errors.Is(err, provider.ErrUnsupported) {
				t.Fatalf("expected ErrUnsupported, got %v", err)
			}
		})
	}
}

func TestStorePolicyRequiresExactMappingOnAdoption(t *testing.T) {
	spec := fixedSpec()
	base, err := composeConfig(spec)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := applyCallbackHostGateway(base, spec, true)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name          string
		policy        bool
		compose       string
		wantErrSubstr string
	}{
		{"enabled accepts mapped", true, mapped, ""},
		{"enabled rejects legacy", true, base, "policy mismatch"},
		{"disabled accepts legacy", false, base, ""},
		{"disabled rejects mapped", false, mapped, "policy mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apps := &truenas.MockAppService{
				GetAppWithConfigFunc: func(_ context.Context, name string) (*truenas.App, error) {
					return appFromCompose(name, "RUNNING", tc.compose), nil
				},
			}
			store := New(apps, &tnclient.MockClient{})
			store.callbackHostGateway = tc.policy
			_, err := store.GetApp(context.Background(), spec.Name)
			if tc.wantErrSubstr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErrSubstr, err)
			}
		})
	}
}

func TestCreateWithCallbackHostGatewayRoundTripsPolicy(t *testing.T) {
	spec := fixedSpec()
	var compose string
	apps := &truenas.MockAppService{
		CreateAppFunc: func(_ context.Context, opts truenas.CreateAppOpts) (*truenas.App, error) {
			compose = opts.CustomComposeConfig
			return &truenas.App{Name: opts.Name}, nil
		},
		GetAppWithConfigFunc: func(_ context.Context, name string) (*truenas.App, error) {
			return appFromCompose(name, "RUNNING", compose), nil
		},
	}
	store := New(apps, &tnclient.MockClient{})
	store.callbackHostGateway = true
	if _, err := store.CreateApp(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compose, "garm.example.invalid:host-gateway") {
		t.Fatalf("created Compose lacks derived host-gateway mapping: %s", compose)
	}
}
