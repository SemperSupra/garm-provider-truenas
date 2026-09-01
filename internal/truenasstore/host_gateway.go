package truenasstore

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"

	truenas "github.com/deevus/truenas-go"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

const labelCallbackHostGateway = "io.sempersupra.garm.callback-host-gateway"

func callbackHostGatewayEntry(callbackURL, metadataURL string) (string, error) {
	callbackHost, err := callbackGatewayHostname("callback URL", callbackURL)
	if err != nil {
		return "", err
	}
	metadataHost, err := callbackGatewayHostname("metadata URL", metadataURL)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(callbackHost, metadataHost) {
		return "", fmt.Errorf("callback and metadata URLs must use the same DNS hostname")
	}
	return strings.ToLower(callbackHost) + ":host-gateway", nil
}

func callbackGatewayHostname(field, raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%s is invalid: %w", field, err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("%s must be an absolute https URL", field)
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "", fmt.Errorf("%s has no hostname", field)
	}
	if net.ParseIP(host) != nil {
		return "", fmt.Errorf("%s must use a DNS hostname, not an IP literal", field)
	}
	return host, nil
}

func applyCallbackHostGateway(compose string, spec provider.AppSpec, enabled bool) (string, error) {
	if !enabled {
		return compose, nil
	}
	entry, err := callbackHostGatewayEntry(spec.CallbackURL, spec.MetadataURL)
	if err != nil {
		return "", fmt.Errorf("callback host-gateway policy: %v: %w", err, provider.ErrUnsupported)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(compose), &doc); err != nil {
		return "", fmt.Errorf("decode fixed runner Compose before host-gateway policy: %w", err)
	}
	services, ok := object(doc["services"])
	if !ok {
		return "", fmt.Errorf("fixed runner Compose has no services object")
	}
	runner, ok := object(services["runner"])
	if !ok {
		return "", fmt.Errorf("fixed runner Compose has no runner service")
	}
	labels, ok := object(runner["labels"])
	if !ok {
		return "", fmt.Errorf("fixed runner Compose has no label object")
	}
	if _, exists := runner["extra_hosts"]; exists {
		return "", fmt.Errorf("fixed runner Compose unexpectedly already contains extra_hosts: %w", provider.ErrUnsupported)
	}

	labels[labelCallbackHostGateway] = "true"
	runner["extra_hosts"] = []string{entry}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("encode fixed runner Compose with host-gateway policy: %w", err)
	}
	return string(encoded), nil
}

func validateCallbackHostGatewayAppConfig(app truenas.App, expected bool) error {
	compose, err := composeObject(app.Config)
	if err != nil {
		return err
	}
	services, ok := object(compose["services"])
	if !ok {
		return fmt.Errorf("managed app config has no services object")
	}
	runner, ok := object(services["runner"])
	if !ok {
		return fmt.Errorf("managed app config has no runner service")
	}
	labels, err := labelMap(runner["labels"])
	if err != nil {
		return err
	}
	labelValue, labelPresent := labels[labelCallbackHostGateway]
	_, extraHostsPresent := runner["extra_hosts"]

	if !expected {
		if labelPresent || extraHostsPresent {
			return fmt.Errorf("managed app carries callback host-gateway state while policy is disabled")
		}
		return nil
	}
	if !labelPresent || labelValue != "true" {
		return fmt.Errorf("managed app is missing enabled callback host-gateway ownership metadata")
	}

	environment, err := stringMap(runner["environment"])
	if err != nil {
		return fmt.Errorf("read managed app environment for callback host-gateway validation: %w", err)
	}
	entry, err := callbackHostGatewayEntry(environment["GARM_CALLBACK_URL"], environment["GARM_METADATA_URL"])
	if err != nil {
		return fmt.Errorf("managed app callback host-gateway URL contract drifted: %w", err)
	}
	extraHosts, err := stringList(runner["extra_hosts"])
	if err != nil {
		return fmt.Errorf("read managed app extra_hosts: %w", err)
	}
	if len(extraHosts) != 1 || extraHosts[0] != entry {
		return fmt.Errorf("managed app callback host-gateway mapping drifted")
	}
	return nil
}

func stringMap(value any) (map[string]string, error) {
	out := map[string]string{}
	switch items := value.(type) {
	case map[string]any:
		for key, raw := range items {
			text, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("environment value %q is not a string", key)
			}
			out[key] = text
		}
	case map[string]string:
		for key, text := range items {
			out[key] = text
		}
	case []any:
		for _, raw := range items {
			text, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("environment list contains a non-string")
			}
			key, val, ok := strings.Cut(text, "=")
			if !ok || key == "" {
				return nil, fmt.Errorf("invalid environment entry %q", text)
			}
			out[key] = val
		}
	default:
		return nil, fmt.Errorf("managed app runner service has no readable environment")
	}
	return out, nil
}

func stringList(value any) ([]string, error) {
	switch items := value.(type) {
	case []any:
		out := make([]string, 0, len(items))
		for _, raw := range items {
			text, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("list contains a non-string")
			}
			out = append(out, text)
		}
		return out, nil
	case []string:
		return append([]string(nil), items...), nil
	default:
		return nil, fmt.Errorf("value is not a string list")
	}
}
