package thordata

import "sort"

var supportedEngines = []string{
	"google", "google_search", "google_light", "google_web", "google_images", "google_videos", "google_news", "google_shopping",
	"google_local", "google_places", "google_product", "google_lens", "google_trends", "google_maps", "google_hotels", "google_jobs",
	"google_scholar", "google_scholar_cite", "google_scholar_author", "google_play", "google_play_product", "google_play_games", "google_play_movies", "google_play_books",
	"google_finance", "google_finance_markets", "google_flights", "google_patents", "google_patents_details", "google_ai_mode",
	"bing", "bing_images", "bing_videos", "bing_news", "bing_shopping", "bing_maps", "yandex", "duckduckgo",
}

var engineAliases = map[string]string{"google_search": "google", "google_places": "google_local"}

var webScraperEngines = map[string]bool{"google_webpage": true, "webpage": true}

var requestParameterNames = []string{
	"adlt", "adults", "after", "age", "ai_overview", "all_reviews", "amenities", "apps_category", "arrival_id", "as_rr", "as_sdt", "as_vis", "as_yhi", "as_ylo", "aspect", "assignee", "author_id", "bags", "bathrooms", "bedrooms", "before", "booking_token", "books_category", "brands", "cat", "cc", "chart", "check_in_date", "check_out_date", "children", "children_ages", "chips", "citation_id", "cites", "cluster", "clustered", "color2", "count", "country", "cp", "cr", "currency", "data", "data_cid", "data_format", "data_type", "date", "deep_search", "departure_id", "departure_token", "device", "df", "direct_link", "dups", "eco_certified", "efirst", "emissions", "end_date", "engine", "exclude_airlines", "exclude_conns", "face", "filter", "filters", "first", "free_cancellation", "free_shipping", "full", "games_category", "geo", "gl", "google_domain", "group", "hl", "hotel_class", "ibp", "image_color", "image_type", "imagesize", "imgar", "imgsz", "include_airlines", "index_market", "infants_in_seat", "infants_on_lap", "input_proxy", "integration_platform", "integration_source", "inventor", "is_logs", "isjson", "json", "kgmid", "kl", "kp", "lang", "language", "lat", "layover_duration", "length", "license", "licenses", "litigation", "ll", "location", "lon", "lr", "lrad", "lsig", "ltype", "ludocid", "max_duration", "max_price", "min_price", "mkt", "multi_city_json", "next_page_token", "nfpr", "no_cache", "num", "offer_id", "offers", "on_sale", "outbound_date", "outbound_time", "p", "page", "page_token", "patent_id", "patents", "period_unit", "period_value", "photo", "place_id", "platform", "price", "product_id", "product_token", "property_token", "property_types", "publication_token", "q", "qft", "rating", "region", "render_js", "resolution", "return_date", "return_json", "return_time", "reviews", "rstr", "safe", "safeSearch", "scholar", "scisbd", "season_id", "section_page_token", "section_token", "see_more_token", "setlang", "shoprs", "show_hidden", "si", "small_businesses", "so", "sort", "sort_by", "source_site", "special_offers", "specs", "start", "start_date", "status", "stops", "store", "store_device", "story_token", "tbm", "tbs", "text", "topic_token", "travel_class", "trend", "type", "tz", "uds", "url", "uule", "vacation_rentals", "view_op", "window", "within", "yandex_domain",
}

var requestParameterSet = func() map[string]bool {
	result := make(map[string]bool, len(requestParameterNames))
	for _, name := range requestParameterNames {
		result[name] = true
	}
	return result
}()

func SupportedEngines() []string { return append([]string(nil), supportedEngines...) }

func RequestParameters() []string {
	result := append([]string(nil), requestParameterNames...)
	sort.Strings(result)
	return result
}
