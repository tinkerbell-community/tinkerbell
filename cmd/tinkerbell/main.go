package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

//go:generate sh -c "cd ../../api && $(cd ../cmd/tinkerbell && go tool -n controller-gen) crd webhook paths=./... output:crd:artifacts:config=../crd/bases"
//go:generate go run ../../script/crdtrim ../../crd/bases/tinkerbell.org_tasks.yaml:v1alpha1
//go:generate sh -c "cd ../../api && $(cd ../cmd/tinkerbell && go tool -n controller-gen) paths=./... object:headerFile=../script/boilerplate.go.txt"
//go:generate go tool buf generate ../.. --config ../../buf.yaml --template ../../buf.gen.yaml --output ../../
func main() {
	ctx, done := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)

	if err := Execute(ctx, done, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err.Error())
		done()
		os.Exit(1)
	}

	done()
}
