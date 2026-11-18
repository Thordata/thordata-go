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
	result, err := client.SearchResponse(ctx, thordata.SearchParams{
		"engine": "google_flights", "departure_id": "SFO", "arrival_id": "JFK",
		"outbound_date": time.Now().AddDate(0, 0, 14).Format("2006-01-02"),
		"return_date":   time.Now().AddDate(0, 0, 21).Format("2006-01-02"),
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("task=%s result=%#v\n", result.TaskID, result.Result)
}
