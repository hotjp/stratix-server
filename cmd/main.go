package main

import (
	"os"
	"os/signal"
	"syscall"

	"stratix-server/config"
	"stratix-server/gateway"
	"github.com/rs/zerolog/log"
)

func main() {
	// Load configuration
	cfg, err := config.Load("config/route.json")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration")
	}

	// Create gateway
	gw, err := gateway.NewGateway(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create gateway")
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Info().Msg("Received shutdown signal")
		
		if err := gw.Stop(); err != nil {
			log.Error().Err(err).Msg("Gateway shutdown error")
		}
	}()

	// Start gateway
	if err := gw.Start(); err != nil {
		log.Fatal().Err(err).Msg("Gateway start error")
	}
}
