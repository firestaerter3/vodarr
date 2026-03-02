package newznab

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vodarr/vodarr/internal/index"
)

// iptvPrefixRe matches IPTV provider prefixes like "┃NL┃", "| NL | HD |", etc.
// Replicates the pattern used in sync/scheduler.go for title cleaning.
var iptvPrefixRe = regexp.MustCompile(`^[\s]*[|┃]\s*(?:[^|┃]+[|┃]\s*)+`)

// stripIPTVPrefix removes leading IPTV category prefixes from a name.
func stripIPTVPrefix(s string) string {
	return strings.TrimSpace(iptvPrefixRe.ReplaceAllString(s, ""))
}

// RSS is the root RSS 2.0 document.
type RSS struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	NZB     string   `xml:"xmlns:nzb,attr"`
	Newznab string   `xml:"xmlns:newznab,attr"`
	Channel Channel  `xml:"channel"`
}

// Channel holds the feed metadata and items.
type Channel struct {
	Title       string   `xml:"title"`
	Description string   `xml:"description"`
	Link        string   `xml:"link"`
	Language    string   `xml:"language"`
	NewznabResp Response `xml:"newznab:response"`
	Items       []Item   `xml:"item"`
}

// Response holds paging info.
type Response struct {
	Offset int `xml:"offset,attr"`
	Total  int `xml:"total,attr"`
}

// GUID is a Newznab RSS guid element with an isPermaLink attribute.
// Setting isPermaLink="false" prevents clients from treating the value as a URL.
type GUID struct {
	Value       string `xml:",chardata"`
	IsPermaLink string `xml:"isPermaLink,attr"`
}

// Item represents a single search result.
type Item struct {
	Title       string    `xml:"title"`
	GUID        GUID      `xml:"guid"`
	Link        string    `xml:"link"`
	PubDate     string    `xml:"pubDate"`
	Description string    `xml:"description"`
	Size        int64     `xml:"size"`
	Enclosure   Enclosure `xml:"enclosure"`
	Attrs       []Attr    `xml:"newznab:attr"`
}

// Enclosure mimics an NZB file link.
type Enclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

// Attr is a newznab:attr element.
type Attr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// CapsResponse is the response to t=caps.
type CapsResponse struct {
	XMLName  xml.Name    `xml:"caps"`
	Server   CapsServer  `xml:"server"`
	Limits   CapsLimits  `xml:"limits"`
	Searching CapsSearcing `xml:"searching"`
	Categories CapsCategories `xml:"categories"`
}

type CapsServer struct {
	Version string `xml:"version,attr"`
	Title   string `xml:"title,attr"`
	Email   string `xml:"email,attr"`
}

type CapsLimits struct {
	Max     int `xml:"max,attr"`
	Default int `xml:"default,attr"`
}

type CapsSearcing struct {
	Search   CapsSearch `xml:"search"`
	TVSearch CapsSearch `xml:"tv-search"`
	Movie    CapsSearch `xml:"movie-search"`
}

type CapsSearch struct {
	Available      string `xml:"available,attr"`
	SupportedParams string `xml:"supportedParams,attr"`
}

type CapsCategories struct {
	Category []CapsCategory `xml:"category"`
}

type CapsCategory struct {
	ID          string          `xml:"id,attr"`
	Name        string          `xml:"name,attr"`
	SubCategory []CapsCategory  `xml:"subcat"`
}

func buildCaps(serverURL string) *CapsResponse {
	return &CapsResponse{
		Server: CapsServer{
			Version: "1.0",
			Title:   "VODarr",
			Email:   "vodarr@localhost",
		},
		Limits: CapsLimits{Max: 100, Default: 100},
		Searching: CapsSearcing{
			Search:   CapsSearch{Available: "yes", SupportedParams: "q,year"},
			TVSearch: CapsSearch{Available: "yes", SupportedParams: "q,tvdbid,tmdbid,season,ep"},
			Movie:    CapsSearch{Available: "yes", SupportedParams: "q,imdbid,tmdbid,year"},
		},
		Categories: CapsCategories{
			Category: []CapsCategory{
				{ID: "2000", Name: "Movies", SubCategory: []CapsCategory{
					{ID: "2040", Name: "HD"},
					{ID: "2045", Name: "UHD"},
					{ID: "2010", Name: "Foreign"},
				}},
				{ID: "5000", Name: "TV", SubCategory: []CapsCategory{
					{ID: "5040", Name: "HD"},
					{ID: "5045", Name: "UHD"},
				}},
			},
		},
	}
}

// buildRSS constructs an RSS feed from pre-expanded RSS items.
// Callers are responsible for pagination slicing; offset/total are for the <newznab:response> element.
func buildRSS(serverURL string, rssItems []Item, offset, total int) *RSS {
	return &RSS{
		Version: "2.0",
		NZB:     "http://www.newzbin.com/DTD/2007/feeds/NZB/",
		Newznab: "http://www.newznab.com/DTD/2010/feeds/attributes/",
		Channel: Channel{
			Title:       "VODarr",
			Description: "Xtream Codes IPTV via VODarr",
			Link:        serverURL,
			Language:    "en-gb",
			NewznabResp: Response{Offset: offset, Total: total},
			Items:       rssItems,
		},
	}
}

// buildMovieRSSItems converts movie index items to RSS items.
func buildMovieRSSItems(serverURL string, items []*index.Item) []Item {
	out := make([]Item, 0, len(items))
	for _, item := range items {
		out = append(out, itemToRSS(serverURL, item))
	}
	return out
}

// buildEpisodeRSSItems expands series index items into per-episode RSS items.
// seasonFilter / epFilter == 0 means no filter.
func buildEpisodeRSSItems(serverURL string, items []*index.Item, seasonFilter, epFilter int) []Item {
	var out []Item
	for _, item := range items {
		if len(item.Episodes) == 0 {
			// No episode data yet (e.g. Xtream fetch failed). In browse mode
			// (no season/ep filter) emit a series-level placeholder so that
			// Prowlarr's sync-test query returns non-zero results. Actual grab
			// requests for these items will fail gracefully in handleGet.
			if seasonFilter == 0 && epFilter == 0 {
				out = append(out, itemToRSS(serverURL, item))
			}
			continue
		}
		for _, ep := range item.Episodes {
			if seasonFilter > 0 && ep.Season != seasonFilter {
				continue
			}
			if epFilter > 0 && ep.EpisodeNum != epFilter {
				continue
			}
			out = append(out, episodeToRSS(serverURL, item, ep))
		}
	}
	return out
}

// episodeToRSS converts a single episode to an RSS item.
func episodeToRSS(serverURL string, series *index.Item, ep index.EpisodeItem) Item {
	size := int64(1024 * 1024 * 1024)
	epTag := fmt.Sprintf("S%02dE%02d", ep.Season, ep.EpisodeNum)
	guid := fmt.Sprintf("vodarr-ep-%d-%d-%d", series.XtreamID, ep.Season, ep.EpisodeNum)
	downloadURL := fmt.Sprintf("%s/api?t=get&id=%d&type=series&episode_id=%d",
		serverURL, series.XtreamID, ep.EpisodeID)

	seriesSafe := strings.ReplaceAll(stripIPTVPrefix(series.Name), " ", ".")
	seriesSafe = strings.ReplaceAll(seriesSafe, ":", "")
	seriesSafe = strings.ReplaceAll(seriesSafe, "/", "")

	ext := ep.Ext
	if ext == "" {
		ext = "mkv"
	}
	title := fmt.Sprintf("%s.%s.WEB-DL.%s", seriesSafe, epTag, ext)

	rssItem := Item{
		Title:       title,
		GUID:        GUID{Value: guid, IsPermaLink: "false"},
		Link:        downloadURL,
		PubDate:     stableDate(series.ReleaseDate),
		Description: series.Plot,
		Size:        size,
		Enclosure: Enclosure{
			URL:    downloadURL,
			Length: size,
			Type:   "application/x-nzb",
		},
		Attrs: []Attr{
			{Name: "category", Value: "5000"},
			{Name: "category", Value: "TV"},
			{Name: "size", Value: strconv.FormatInt(size, 10)},
			{Name: "season", Value: strconv.Itoa(ep.Season)},
			{Name: "episode", Value: strconv.Itoa(ep.EpisodeNum)},
		},
	}

	if series.TVDBId != "" {
		rssItem.Attrs = append(rssItem.Attrs, Attr{Name: "tvdbid", Value: series.TVDBId})
	}
	if series.TMDBId != "" {
		rssItem.Attrs = append(rssItem.Attrs, Attr{Name: "tmdbid", Value: series.TMDBId})
	}
	if series.IMDBId != "" {
		rssItem.Attrs = append(rssItem.Attrs, Attr{Name: "imdbid", Value: series.IMDBId})
	}

	return rssItem
}

func itemToRSS(serverURL string, item *index.Item) Item {
	size := int64(1024 * 1024 * 1024) // 1 GB placeholder
	category := "5000"
	categoryName := "TV"
	if item.Type == index.TypeMovie {
		category = "2000"
		categoryName = "Movies"
	}

	guid := fmt.Sprintf("vodarr-%s-%d", item.Type, item.XtreamID)
	downloadURL := fmt.Sprintf("%s/api?t=get&id=%d&type=%s", serverURL, item.XtreamID, item.Type)

	title := buildTitle(item)

	rssItem := Item{
		Title:       title,
		GUID:        GUID{Value: guid, IsPermaLink: "false"},
		Link:        downloadURL,
		PubDate:     stableDate(item.ReleaseDate),
		Description: item.Plot,
		Size:        size,
		Enclosure: Enclosure{
			URL:    downloadURL,
			Length: size,
			Type:   "application/x-nzb",
		},
		Attrs: []Attr{
			{Name: "category", Value: category},
			{Name: "category", Value: categoryName},
			{Name: "size", Value: strconv.FormatInt(size, 10)},
		},
	}

	if item.IMDBId != "" {
		rssItem.Attrs = append(rssItem.Attrs, Attr{Name: "imdb", Value: strings.TrimPrefix(item.IMDBId, "tt")})
		rssItem.Attrs = append(rssItem.Attrs, Attr{Name: "imdbid", Value: item.IMDBId})
	}
	if item.TVDBId != "" {
		rssItem.Attrs = append(rssItem.Attrs, Attr{Name: "tvdbid", Value: item.TVDBId})
	}
	if item.TMDBId != "" {
		rssItem.Attrs = append(rssItem.Attrs, Attr{Name: "tmdbid", Value: item.TMDBId})
	}
	if item.Year != "" {
		rssItem.Attrs = append(rssItem.Attrs, Attr{Name: "year", Value: item.Year})
	}
	if item.Rating > 0 {
		rssItem.Attrs = append(rssItem.Attrs, Attr{Name: "rating", Value: fmt.Sprintf("%.1f", item.Rating)})
	}

	return rssItem
}

// stableDate returns a consistent RFC1123Z timestamp from a release date string.
// Using a stable date (rather than time.Now) prevents Sonarr/Radarr from
// re-grabbing already-processed items on each RSS sync.
func stableDate(releaseDate string) string {
	if releaseDate != "" {
		for _, layout := range []string{"2006-01-02", "2006"} {
			if t, err := time.Parse(layout, releaseDate); err == nil {
				return t.UTC().Format(time.RFC1123Z)
			}
		}
	}
	return time.Unix(0, 0).UTC().Format(time.RFC1123Z)
}

func buildTitle(item *index.Item) string {
	ext := item.ContainerExt
	if ext == "" {
		ext = "mkv"
	}
	safe := strings.ReplaceAll(stripIPTVPrefix(item.Name), " ", ".")
	safe = strings.ReplaceAll(safe, ":", "")
	safe = strings.ReplaceAll(safe, "/", "")

	year := item.Year
	if year == "" && item.ReleaseDate != "" && len(item.ReleaseDate) >= 4 {
		year = item.ReleaseDate[:4]
	}

	if year != "" {
		return fmt.Sprintf("%s.%s.WEB-DL.%s", safe, year, ext)
	}
	return fmt.Sprintf("%s.WEB-DL.%s", safe, ext)
}
