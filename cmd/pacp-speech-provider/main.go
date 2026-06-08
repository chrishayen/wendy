package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"pacp/internal/provider/speech"
)

type engineConfigFile struct {
	TTSCommand []string `json:"tts_command"`
	STTCommand []string `json:"stt_command"`
}

func main() {
	addr := flag.String("addr", "localhost:18091", "listen address")
	endpoint := flag.String("endpoint", os.Getenv("PACP_PROVIDER_ENDPOINT"), "provider endpoint advertised in the manifest")
	serviceID := flag.String("service-id", speech.DefaultServiceID, "provider service id")
	serviceName := flag.String("service-name", "Speech Provider", "provider service name")
	ttsCapabilityID := flag.String("tts-capability-id", speech.DefaultTTSCapabilityID, "TTS capability id")
	sttCapabilityID := flag.String("stt-capability-id", speech.DefaultSTTCapabilityID, "STT capability id")
	voiceCatalogPath := flag.String("voice-catalog", "", "optional voice/model/format catalog JSON path")
	engineConfigPath := flag.String("engine-config", "", "optional engine command config JSON path")
	dryRun := flag.Bool("dry-run", false, "return deterministic TTS/STT responses without engine commands")
	timeout := flag.Duration("timeout", time.Minute, "speech engine timeout")
	flag.Parse()

	engineConfig, err := loadEngineConfig(*engineConfigPath)
	if err != nil {
		log.Fatal(err)
	}
	advertisedEndpoint := *endpoint
	if advertisedEndpoint == "" {
		advertisedEndpoint = defaultEndpoint(*addr)
	}
	server, err := speech.NewServer(speech.Config{
		Endpoint:         advertisedEndpoint,
		ServiceID:        *serviceID,
		ServiceName:      *serviceName,
		TTSCapabilityID:  *ttsCapabilityID,
		STTCapabilityID:  *sttCapabilityID,
		VoiceCatalogPath: *voiceCatalogPath,
		DryRun:           *dryRun,
		TTSCommand:       engineConfig.TTSCommand,
		STTCommand:       engineConfig.STTCommand,
		Timeout:          *timeout,
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("serving speech provider addr=%s dry_run=%t", *addr, *dryRun)
	if err := http.ListenAndServe(*addr, server); err != nil {
		log.Fatal(err)
	}
}

func loadEngineConfig(path string) (engineConfigFile, error) {
	if strings.TrimSpace(path) == "" {
		return engineConfigFile{}, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return engineConfigFile{}, err
	}
	var cfg engineConfigFile
	if err := json.Unmarshal(body, &cfg); err != nil {
		return engineConfigFile{}, err
	}
	return cfg, nil
}

func defaultEndpoint(addr string) string {
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}
	return "http://" + addr
}
