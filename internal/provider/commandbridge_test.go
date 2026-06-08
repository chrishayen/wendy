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

func TestCommandBridgeExposesInvokeContextEnvironment(t *testing.T) {
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
	rec := invokeBridge(t, server, contracts.ProviderInvokeRequest{
		Input: map[string]any{"message": "hello"},
		Context: contracts.ProviderInvokeContext{
			SubjectID:       "sub_agent",
			RequestID:       "req_command",
			JobID:           "job_1",
			ResourceLeaseID: "lease_1",
			ArtifactBaseURL: "http://artifacts.local",
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	output := commandBridgeOutput(t, rec.Body.Bytes())
	expected := map[string]any{
		"token":                 "env-token",
		"request_id_env":        "req_command",
		"subject_id_env":        "sub_agent",
		"job_id_env":            "job_1",
		"resource_lease_id_env": "lease_1",
		"artifact_base_url_env": "http://artifacts.local",
	}
	for key, want := range expected {
		if output[key] != want {
			t.Fatalf("output[%s] = %#v want %#v in %#v", key, output[key], want, output)
		}
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

func commandBridgeOutput(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var envelope contracts.SuccessEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := envelope.Data.(map[string]any)
	return data["output"].(map[string]any)
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
	for outputName, envName := range map[string]string{
		"request_id_env":        "PACP_REQUEST_ID",
		"subject_id_env":        "PACP_SUBJECT_ID",
		"job_id_env":            "PACP_JOB_ID",
		"resource_lease_id_env": "PACP_RESOURCE_LEASE_ID",
		"artifact_base_url_env": "PACP_ARTIFACT_BASE_URL",
	} {
		if value := os.Getenv(envName); value != "" {
			response.Output[outputName] = value
		}
	}
	var raw bytes.Buffer
	_ = json.NewEncoder(&raw).Encode(response)
	_, _ = os.Stdout.Write(raw.Bytes())
}
