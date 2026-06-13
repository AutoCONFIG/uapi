package main

import (
	"log"

	"github.com/AutoCONFIG/uapi-helper/internal/helperapp"
)

func main() {
	app, err := helperapp.NewApp()
	if err != nil {
		log.Fatal(err)
	}
	app.Run()
}
