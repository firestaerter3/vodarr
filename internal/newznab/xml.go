package newznab

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vodarr/vodarr/internal/index"
)

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

// Item represents a single search result.
type Item struct {
	Title       string  `xml:"title"`
	GUID        string  `xml:"guid"`
	Link        string  `xml:"link"`
	PubDate     string  `xml:"pubDate"`
	Description string  `xml:"description"`
	Size        int64   `xml:"size"`
	Enclosure   Enclosure `xml:"enclosure"`
	Attrs       []Attr  `xml:"newznab:attr"`
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
		Limits: CapsLimits{Max: 100, Default: 50},
		Searching: CapsSearcing{
			Search:   CapsSearch{Available: "yes", SupportedParams: "q"},
			TVSearch: CapsSearch{Available: "yes", SupportedParams: "q,tvdbid,season,ep"},
			Movie:    CapsSearch{Available: "yes", SupportedParams: "q,imdbid"},
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

func buildRSS(serverURL string, items []*index.Item, offset, total int) *RSS {
	rssItems := make([]Item, 0, len(items))
	for _, item := range items {
		rssItems = append(rssItems, itemToRSS(serverURL, item))
	}

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
		GUID:        guid,
		Link:        downloadURL,
		PubDate:     time.Now().UTC().Format(time.RFC1123Z),
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

func buildTitle(item *index.Item) string {
	ext := item.ContainerExt
	if ext == "" {
		ext = "mkv"
	}
	safe := strings.ReplaceAll(item.Name, " ", ".")
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
