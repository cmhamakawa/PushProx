package main

import (
	"net/http"

	"goproxy_pkg"
)

func main() {
	http.ListenAndServe("localhost:8080", &goproxy.Goproxy{})
}