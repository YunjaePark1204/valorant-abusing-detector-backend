package main

import (
	"bufio"
	"log"
	"net/http"
	"os"
	"strings"

	handler "github.com/YunjaePark1204/valorant-abusing-detector-backend/api"
)

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.Trim(strings.TrimSpace(line[idx+1:]), `"'`)
		if os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
}

func main() {
	loadDotEnv(".env")
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Backend server listening on http://localhost:%s", port)
	if err := http.ListenAndServe(":"+port, http.HandlerFunc(handler.Handler)); err != nil {
		log.Fatal(err)
	}
}
