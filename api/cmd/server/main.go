package main

import (
	"log"
	"net/http"
	"os"

	"github.com/francorobles/fritz/api/internal/httpapi"
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("fritz-api listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, httpapi.NewRouter()))
}
