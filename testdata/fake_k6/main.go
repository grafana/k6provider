package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("k6 v0.50.0 (test binary)")
		os.Exit(0)
	}
	os.Exit(1)
}
