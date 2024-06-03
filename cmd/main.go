package main

import (
	"fmt"
	"log"
	"os"

	"github.com/kimle/fpl-form/pkg/table"
)

func main() {
	fn := os.Getenv("FUNCTION_NAME")
	fmt.Println(fn)
	if err := table.Table(); err != nil {
		log.Fatal(err)
	}
}
