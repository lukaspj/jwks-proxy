package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	configPath := flag.String("config", os.Getenv("JWKS_PROXY_CONFIG"), "path to config file")
	flag.Parse()
	if *configPath == "" {
		*configPath = "config.json"
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	addr := cfg.Listen
	fmt.Printf("jwks-proxy listening on %s (external: %s, routes: %d)\n", addr, cfg.ExternalURL, len(cfg.Routes))
	if err := http.ListenAndServe(addr, NewProxy(cfg).Handler()); err != nil {
		log.Fatal(err)
	}
}
