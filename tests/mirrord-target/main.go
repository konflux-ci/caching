package main

import "C"

import (
	"log"
	"net/http"
)

func main() {
	log.Fatal(http.ListenAndServe(":9090", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
}
