package main

import (
	"context"
	"fmt"
	"log"
	"time"

	thordata "github.com/Thordata/thordata-go"
)

func main() {
	client, err := thordata.NewClient(thordata.ClientOptions{})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := client.Search(ctx, thordata.SearchParams{"query": "latest AI agent research", "country": "us", "language": "en"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%#v\n", result)
}
