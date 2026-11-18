# Thordata Go SDK

The Thordata Go SDK provides a context-aware client for the Thordata SERP API and seven verified Web Scraper products. It uses Go's standard library, returns engine-native records, and supports request cancellation with `context.Context`.

Build dependable web data workflows in Go with a client designed for services, automation, and data infrastructure. Thordata Go SDK brings live search and structured web scraping into applications that need fresh data without maintaining a separate integration for every target.

Use it for search intelligence, product and market monitoring, business-data enrichment, RAG pipelines, and AI agent workflows. Context-aware requests make it easier to fit data collection into existing Go services and operational controls.

## Why Thordata Go SDK

- **Search and scrape through one client**: Query live search engines and collect structured records through supported Web Scraper actions.
- **Native Go workflow**: Use `context.Context`, standard-library networking, and engine-native result shapes in services and automation.
- **Built for controlled execution**: Cancel requests locally, enforce deadlines, and preserve typed errors for application-level handling.
- **Ready for downstream systems**: Send structured results into analytics, enrichment, RAG, agents, and internal data pipelines.
- **Production-minded data handling**: Benefit from validated downloads, redacted diagnostics, bounded polling, and explicit business-error classification.

Create a Thordata account to get started:

- [Get started with Thordata](https://www.thordata.com/?ls=github&lk=thordata)
- [Read the official documentation](https://doc.thordata.com)

## Requirements and installation

- Go 1.22 or newer.
- A Thordata API key for live requests.
- No runtime third-party dependencies.

Install the module with Go 1.22 or newer:

```sh
go get github.com/Thordata/thordata-go
```

Then import the package as `github.com/Thordata/thordata-go` (package name `thordata`).

Set `THORDATA_API_KEY`, `THORDATA_API_TOKEN`, or the compatible `TALORDATA_API_TOKEN` environment variable. `ClientOptions.APIKey`, `APIToken`, and `Token` take precedence, in that order. An explicitly supplied blank key is rejected instead of falling back to the environment.

```go
client, err := thordata.NewClient(thordata.ClientOptions{})
if err != nil {
	log.Fatal(err)
}
defer client.Close()
```

## SERP search

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

result, err := client.Search(ctx, thordata.SearchParams{
	"engine": "google_search",
	"query":  "latest AI agent research",
	"country": "us",
	"language": "en",
})
if err != nil {
	log.Fatal(err)
}
fmt.Printf("%#v\n", result)
```

`Search` returns the decoded result. `SearchJSON` is a compatibility alias. Use `SearchResponse` when you also need `TaskID` and the original decoded response envelope. These methods check both HTTP status and the Thordata business code.

`RawSearch` returns the HTTP 2xx response body unchanged, including non-JSON text. It does **not** interpret business codes. For example, HTTP 200 with `{"code":0,"data":"error, Collection failed"}` returns that exact body and `err == nil`; the same response through `Search` returns `NotCollectedError`. A nil error from `RawSearch` confirms only transport/HTTP success, not successful data collection. Transport failures and non-2xx HTTP responses still return errors. This matches the Python raw-search contract; use `Search` or `SearchResponse` when you need business-error classification.

`Request` exposes lower-level form/query requests without HTTP/business error classification and leaves body parsing to the caller; close the returned response body.

Supported engines are returned by `thordata.SupportedEngines()`. The 38 accepted names include the `google_search` → `google` and `google_places` → `google_local` aliases (36 canonical engines). This list describes the SDK's accepted inputs, not an upstream availability or collection-success guarantee. Supply each engine's required parameters, such as `product_id` for `google_product`. A valid request may still fail upstream; `Search` reports the service error. The 194-name request parameter allowlist is available through `thordata.RequestParameters()`.

Aliases map `query`/`country`/`language` to `q`/`gl`/`hl`; explicit canonical values win. Yandex maps `q` to `text`. Unknown parameters are ignored. Boolean SERP values become `1` or `0`, and object/list parameters are JSON-encoded. `json=1` and `isjson=1` are defaults but explicit values are preserved. Results retain each engine's native shape.

A business-success response can contain only metadata or empty results. `Search` does not require engine-specific content fields or invent missing data; validate the fields your application needs, including quote data when using `google_finance`. Successful SERP `Result`/`Raw` and raw responses preserve the upstream business data; credential redaction applies to error diagnostics and Web Scraper results.

## Web Scraper

The initial SDK supports these spider IDs:

- `linkedin_company_information_by-url`: `url`
- `linkedin_job_listings_information_by-job-url`: `job_url`
- `youtube_video-post_by-keyword`: `keyword`, optional `num_of_posts`
- `facebook_profile_by-profiles-url`: `url`
- `youtube_video-post_by-explore`: `url`, optional `all_tabs`
- `twitter_profile_by-profileurl`: `url`
- `amazon_product_by-category-url`: `url`, `page_turning`, optional `sort_by`

```go
records, err := client.Scrape(ctx,
	"linkedin_company_information_by-url",
	thordata.WebScraperParams{"url": "https://www.linkedin.com/company/microsoft"},
)
if err != nil {
	log.Fatal(err)
}
fmt.Printf("%#v\n", records)
```

`ScrapeResponse` additionally returns the task ID, spider ID, status (`completed`, `partial`, or `empty`), total record count, failed-record count, and redacted lifecycle diagnostics. The default maximum wait is 300 seconds with five-second polling. `ScrapeOptions.MaxWait` must be a whole number of seconds from 1 to 300; zero selects the default of 300 seconds. For example, `90*time.Second` is valid, while `500*time.Millisecond`, `1500*time.Millisecond`, and `301*time.Second` are rejected before any request. Use `context.WithTimeout` for a shorter or fractional-second deadline, or cancel the context to stop local waiting; this does not cancel a server task that has already been submitted. A request-level timeout defaults to 30 seconds.

`IncludeErrors` defaults to true. Use `thordata.Bool(false)` to skip error-record diagnostics. `FileName` defaults to `{{TasksID}}`. The SDK rejects unknown spider parameters before sending a request. Amazon `sort_by` accepts `Best Sellers`, `Newest Arrivals`, `Avg. Customer Review`, `Price: High to Low`, `Price: Low to High`, or `Featured`. For wire compatibility, `all_tabs: false` is sent as `flase`.

Downloads are restricted to the validated Thordata download origin, path, task ID, JSON type, and query parameters. Redirects and credential-bearing custom headers are not forwarded during the download.

## Errors and cancellation

Errors are concrete Go types and support `errors.As`/`errors.Is`. Common types include `ConfigurationError`, `ConnectionError`, `TimeoutError`, `APIError`, `AuthenticationError`, `PermissionError`, `RateLimitError`, `InvalidRequestError`, `NotCollectedError`, and `WebScraperError`. `APIError` retains the service code, HTTP status, and redacted response data. `WebScraperError` retains the stage and safe task metadata. A business code may fail even when the HTTP status is 200.

```go
var apiErr *thordata.APIError
if errors.As(err, &apiErr) {
	fmt.Printf("service code=%v, HTTP status=%d\n", apiErr.Code, apiErr.HTTPStatus)
}
```

## Local checks and examples

```sh
go test ./...
go vet ./...
go build ./...
go test -race ./...
```

Tests use local HTTP transports and copied shared contract fixtures; they do not send requests or consume account credits. The runnable examples in `examples/` require a valid API key and make real API calls.

## Compatibility notes

This SDK targets the combined behavior of the current Python and JavaScript clients while following Go conventions. It uses synchronous methods and `context.Context` instead of explicit async methods. It supports the union of their request parameter lists. `Search` requires a JSON object response; use `RawSearch` to access an HTTP 2xx body without JSON or business-code checks. `Request` supports explicit absolute URLs, but sends API credentials only to the configured API origin. The Go platform source header/form value is `go`. Production smoke calls on 2026-11-16 exercised SERP and Web Scraper with that value; they do not guarantee future upstream availability or verify how the server stores source statistics.

## Start Building

Start with a context-aware SERP request, add a Web Scraper action when you need structured records, and connect the results to the Go services and pipelines your team already runs. The SDK is designed to fit controlled, cancellable data workflows from the first prototype onward.

[Explore Thordata SERP API](https://www.thordata.com/products/serp-api?op=flora&from=github) | [Explore Thordata Web Scraper](https://www.thordata.com/products/web-scraper?op=flora&from=github)
