//go:build !js && !wasm

package main

import (
	"fmt"
	"net/http"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
	"google.golang.org/grpc"
)

// main demonstrates a standalone consumer module that imports GoGRPCBridge without local replace directives.
func main() {
	parseGRPCServer := grpc.NewServer()
	parseMux := http.NewServeMux()

	parseMux.Handle("/grpc", grpctunnel.Wrap(parseGRPCServer, grpctunnel.WithOriginCheck(func(parseRequest *http.Request) bool {
		return parseRequest.Header.Get("Origin") == ""
	})))

	fmt.Println("bridge handler mounted at /grpc")
}
