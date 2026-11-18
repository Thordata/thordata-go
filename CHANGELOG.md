# Changelog

## Unreleased

- Clarified that `RawSearch` preserves HTTP 2xx bodies without checking business codes; added contract tests alongside `Search` error classification.
- Documented the integer-second `MaxWait` constraint and improved its validation message without changing the accepted values.
- Clarified private-module installation requirements, engine-name support versus upstream availability, and caller responsibility for validating result content.

## 0.1.0

- Added a context-aware Go client for Thordata SERP requests and seven verified Web Scraper products.
- Added typed API and lifecycle errors, task metadata, result status counts, and credential redaction.
- Added shared contract fixtures, offline HTTP tests, and search/scraper examples.
