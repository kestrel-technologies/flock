// Command flockd is the Flock daemon.
package main

import (
	"flag"
	"log"

	"kestrel.example/flock/internal/config"
)

func main() {
	tenantsPath := flag.String("tenants", "config/tenants.yaml", "tenant configuration")
	flag.Parse()

	tenants, err := config.LoadTenants(*tenantsPath)
	if err != nil {
		log.Fatalf("flockd: %v", err)
	}
	for _, t := range tenants.All() {
		log.Printf("tenant %s: connector=%s enabled=%v", t.Name, t.Connector, t.Enabled)
	}
	// TODO(ben.castillo): connectors and the reconcile loop (FLOCK-12).
}
