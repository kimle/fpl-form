package main

import (
	"log"

	"github.com/kimle/fpl-form/pkg/table"
)

func main() {
	if err := table.Table(); err != nil {
		log.Fatal(err)
	}
}
