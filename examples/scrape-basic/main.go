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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	result, err := client.ScrapeResponse(ctx, "linkedin_company_information_by-url", thordata.WebScraperParams{
		"url": "https://www.linkedin.com/company/microsoft",
	}, thordata.ScrapeOptions{MaxWait: 4 * time.Minute})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("task=%s status=%s records=%d failed=%d\n", result.TaskID, result.Status, result.RecordCount, result.ErrorRecordCount)
	fmt.Printf("%#v\n", result.Result)
}
