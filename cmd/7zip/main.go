package main

import (
	"os"

	"github.com/inpadi/7zip/internal/app"
)

func main() {
	os.Exit(app.RunWithIO(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
