package provider

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"pacp/internal/contracts"
)

func TestCommandBridgeRunsConfiguredCommand(t *testing.T) {
	server, err := NewCommandBridgeServer(bridgeManifest(), CommandBridgeConfig{
		Routes: map[string]CommandBridgeRoute{
			"cap_bridge_echo": {
				Command: helperCommand(t, "echo"),
			},
		},
	})
	if err != nil {
		t.Fatalf("new command bridge: %v", err)
	}

	rec := invokeBridge(t, server, contracts.ProviderInvokeRequest{
		Input:   map[string]any{"message": "hello"},
		Context: contracts.ProviderInvokeContext{SubjectID: "sub_agent"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope contracts.SuccessEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := envelope.Data.(map[string]any)
	output := data["output"].(map[string]any)
	if output["message"] != "hello" || output["subject_id"] != "sub_agent" {
		t.Fatalf("output = %#v", output)
	}
}

func TestCommandBridgeSupportsEnvironmentFromEnv(t *testing.T) {
	t.Setenv("PACP_TEST_COMMAND_TOKEN", "env-token")
	server, err := NewCommandBridgeServer(bridgeManifest(), CommandBridgeConfig{
		Routes: map[string]CommandBridgeRoute{
			"cap_bridge_echo": {
				Command:            helperCommand(t, "env"),
				EnvironmentFromEnv: map[string]string{"BACKEND_TOKEN": "PACP_TEST_COMMAND_TOKEN"},
			},
		},
	})
	if err != nil {
		t.Fatalf("new command bridge: %v", err)
	}
	rec := invokeBridge(t, server, contracts.ProviderInvokeRequest{Input: map[string]any{"message": "hello"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCommandBridgeReportsCommandFailure(t *testing.T) {
	server, err := NewCommandBridgeServer(bridgeManifest(), CommandBridgeConfig{
		Routes: map[string]CommandBridgeRoute{
			"cap_bridge_echo": {
				Command: helperCommand(t, "fail"),
			},
		},
	})
	if err != nil {
		t.Fatalf("new command bridge: %v", err)
	}
	rec := invokeBridge(t, server, contracts.ProviderInvokeRequest{Input: map[string]any{"message": "hello"}})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCommandBridgeRequiresRoutes(t *testing.T) {
	_, err := NewCommandBridgeServer(bridgeManifest(), CommandBridgeConfig{Routes: map[string]CommandBridgeRoute{}})
	if err == nil {
		t.Fatal("expected missing route error")
	}
}

func TestCommandBridgeRejectsMissingEnvSource(t *testing.T) {
	_, err := NewCommandBridgeServer(bridgeManifest(), CommandBridgeConfig{
		Routes: map[string]CommandBridgeRoute{
			"cap_bridge_echo": {
				Command:            helperCommand(t, "echo"),
				EnvironmentFromEnv: map[string]string{"BACKEND_TOKEN": "PACP_TEST_MISSING_COMMAND_TOKEN"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected missing env source error")
	}
}

func helperCommand(t *testing.T, mode string) []string {
	t.Helper()
	return []string{os.Args[0], "-test.run=TestCommandBridgeHelperProcess", "--", mode}
}

func TestCommandBridgeHelperProcess(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	var req contracts.ProviderInvokeRequest
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		_, _ = os.Stderr.WriteString(err.Error())
		os.Exit(2)
	}
	switch mode {
	case "echo":
		writeCommandBridgeHelperResponse(req, "")
		os.Exit(0)
	case "env":
		writeCommandBridgeHelperResponse(req, os.Getenv("BACKEND_TOKEN"))
		os.Exit(0)
	case "fail":
		_, _ = os.Stderr.WriteString("command failed")
		os.Exit(3)
	default:
		_, _ = os.Stderr.WriteString("unknown helper mode")
		os.Exit(4)
	}
}

func writeCommandBridgeHelperResponse(req contracts.ProviderInvokeRequest, token string) {
	response := contracts.ProviderInvokeResponse{
		Output: map[string]any{
			"message":    req.Input["message"],
			"subject_id": req.Context.SubjectID,
		},
	}
	if token != "" {
		response.Output["token"] = token
	}
	var raw bytes.Buffer
	_ = json.NewEncoder(&raw).Encode(response)
	_, _ = os.Stdout.Write(raw.Bytes())
}
