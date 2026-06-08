package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"pacp/internal/contracts"
)

type CommandBridgeConfig struct {
	Routes map[string]CommandBridgeRoute `json:"routes"`
}

type CommandBridgeRoute struct {
	Command            []string          `json:"command"`
	WorkingDirectory   string            `json:"working_directory,omitempty"`
	Environment        map[string]string `json:"environment,omitempty"`
	EnvironmentFromEnv map[string]string `json:"environment_from_env,omitempty"`
	TimeoutSeconds     int               `json:"timeout_seconds,omitempty"`
}

func NewCommandBridgeServer(manifest contracts.ProviderManifest, cfg CommandBridgeConfig) (*Server, error) {
	handlers := map[string]CapabilityHandler{}
	for _, capability := range manifest.Capabilities {
		route, ok := cfg.Routes[capability.ID]
		if !ok {
			return nil, fmt.Errorf("%w: route missing for capability %s", ErrValidation, capability.ID)
		}
		normalized, err := normalizeCommandBridgeRoute(route)
		if err != nil {
			return nil, fmt.Errorf("%w: route %s: %s", ErrValidation, capability.ID, err)
		}
		handlers[capability.ID] = commandBridgeHandler(normalized)
	}
	return NewServer(manifest, handlers)
}

func normalizeCommandBridgeRoute(route CommandBridgeRoute) (CommandBridgeRoute, error) {
	if len(route.Command) == 0 {
		return CommandBridgeRoute{}, errors.New("command is required")
	}
	for i, part := range route.Command {
		if strings.TrimSpace(part) == "" {
			return CommandBridgeRoute{}, fmt.Errorf("command[%d] must not be empty", i)
		}
	}
	if route.Environment == nil {
		route.Environment = map[string]string{}
	}
	for name, envName := range route.EnvironmentFromEnv {
		if strings.TrimSpace(name) == "" {
			return CommandBridgeRoute{}, errors.New("environment_from_env variable name is required")
		}
		if strings.TrimSpace(envName) == "" {
			return CommandBridgeRoute{}, fmt.Errorf("environment_from_env %s source env var is required", name)
		}
		value, ok := os.LookupEnv(envName)
		if !ok {
			return CommandBridgeRoute{}, fmt.Errorf("environment variable %s is not set", envName)
		}
		route.Environment[name] = value
	}
	return route, nil
}

func commandBridgeHandler(route CommandBridgeRoute) CapabilityHandler {
	return func(ctx context.Context, req contracts.ProviderInvokeRequest) (contracts.ProviderInvokeResponse, error) {
		if route.TimeoutSeconds > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(route.TimeoutSeconds)*time.Second)
			defer cancel()
		}
		body, err := json.Marshal(req)
		if err != nil {
			return contracts.ProviderInvokeResponse{}, err
		}
		cmd := exec.CommandContext(ctx, route.Command[0], route.Command[1:]...)
		if route.WorkingDirectory != "" {
			cmd.Dir = route.WorkingDirectory
		}
		cmd.Env = commandEnvironment(route.Environment)
		cmd.Stdin = bytes.NewReader(body)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			message := strings.TrimSpace(stderr.String())
			if message == "" {
				message = err.Error()
			}
			return contracts.ProviderInvokeResponse{}, fmt.Errorf("%w: %s", ErrBackend, message)
		}
		return decodeHTTPBridgeResponse(bytes.NewReader(stdout.Bytes()))
	}
}

func commandEnvironment(values map[string]string) []string {
	env := os.Environ()
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}
