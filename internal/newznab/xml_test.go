package newznab

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/vodarr/vodarr/internal/index"
)

func TestBuildRSSMovie(t *testing.T) {
	items := []*index.Item{
		{
			Type:     index.TypeMovie,
			XtreamID: 42,
			Name:     "The Matrix",
			Year:     "1999",
			IMDBId:   "tt0133093",
			TMDBId:   "603",
		},
	}
	rssItems := buildMovieRSSItems("http://localhost:7878", items)
	rss := buildRSS("http://localhost:7878", rssItems, 0, len(rssItems))

	if len(rss.Channel.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(rss.Channel.Items))
	}

	item := rss.Channel.Items[0]
	if !strings.Contains(item.Title, "Matrix") {
		t.Errorf("title %q should contain 'Matrix'", item.Title)
	}
	if !strings.HasSuffix(item.Title, ".mkv") {
		t.Errorf("title %q should end with .mkv", item.Title)
	}
	if !strings.Contains(item.Title, "1999") {
		t.Errorf("title %q should contain year", item.Title)
	}

	// Check IMDB attr
	var foundIMDB bool
	for _, attr := range item.Attrs {
		if attr.Name == "imdbid" && attr.Value == "tt0133093" {
			foundIMDB = true
		}
	}
	if !foundIMDB {
		t.Error("missing imdbid attr")
	}
}

func TestBuildRSSCategory(t *testing.T) {
	movie := &index.Item{Type: index.TypeMovie, XtreamID: 1, Name: "Movie"}
	series := &index.Item{
		Type:     index.TypeSeries,
		XtreamID: 2,
		Name:     "Series",
		Episodes: []index.EpisodeItem{{EpisodeID: 1, Season: 1, EpisodeNum: 1}},
	}

	movieItems := buildMovieRSSItems("http://localhost", []*index.Item{movie})
	seriesItems := buildEpisodeRSSItems("http://localhost", []*index.Item{series}, 0, 0)

	rssMovie := buildRSS("http://localhost", movieItems, 0, len(movieItems))
	rssSeries := buildRSS("http://localhost", seriesItems, 0, len(seriesItems))

	checkCat := func(items []Item, expected string) {
		for _, item := range items {
			for _, attr := range item.Attrs {
				if attr.Name == "category" && attr.Value == expected {
					return
				}
			}
		}
		t.Errorf("expected category %q not found in attrs", expected)
	}
	checkCat(rssMovie.Channel.Items, "2000")
	checkCat(rssSeries.Channel.Items, "5000")
}

func TestBuildRSSEpisodeAttrs(t *testing.T) {
	series := &index.Item{
		Type:     index.TypeSeries,
		XtreamID: 10,
		Name:     "Breaking Bad",
		TVDBId:   "81189",
		Episodes: []index.EpisodeItem{
			{EpisodeID: 101, Season: 1, EpisodeNum: 1, Title: "Pilot", Ext: "mkv"},
			{EpisodeID: 102, Season: 1, EpisodeNum: 2, Title: "Cat's in the Bag", Ext: "mkv"},
			{EpisodeID: 201, Season: 2, EpisodeNum: 1, Title: "Seven Thirty-Seven", Ext: "mkv"},
		},
	}

	// All episodes
	allItems := buildEpisodeRSSItems("http://localhost:7878", []*index.Item{series}, 0, 0)
	if len(allItems) != 3 {
		t.Fatalf("expected 3 episode items, got %d", len(allItems))
	}

	// Check season/episode attrs on first item
	item := allItems[0]
	var foundSeason, foundEpisode bool
	for _, attr := range item.Attrs {
		if attr.Name == "season" && attr.Value == "1" {
			foundSeason = true
		}
		if attr.Name == "episode" && attr.Value == "1" {
			foundEpisode = true
		}
	}
	if !foundSeason {
		t.Error("missing season attr")
	}
	if !foundEpisode {
		t.Error("missing episode attr")
	}

	// Filter by season
	s2Items := buildEpisodeRSSItems("http://localhost:7878", []*index.Item{series}, 2, 0)
	if len(s2Items) != 1 {
		t.Fatalf("season 2 filter: expected 1 item, got %d", len(s2Items))
	}

	// Filter by season+ep
	s1e2Items := buildEpisodeRSSItems("http://localhost:7878", []*index.Item{series}, 1, 2)
	if len(s1e2Items) != 1 {
		t.Fatalf("season 1 ep 2 filter: expected 1 item, got %d", len(s1e2Items))
	}
	if !strings.Contains(s1e2Items[0].Title, "S01E02") {
		t.Errorf("title %q should contain S01E02", s1e2Items[0].Title)
	}

	// Download URL should contain episode_id
	if !strings.Contains(s1e2Items[0].Link, "episode_id=102") {
		t.Errorf("link %q should contain episode_id=102", s1e2Items[0].Link)
	}

	// All episode items must have isPermaLink="false" — prevents clients from
	// treating the GUID as a URL and requesting it (which would 404).
	for i, epItem := range allItems {
		if epItem.GUID.IsPermaLink != "false" {
			t.Errorf("episode item[%d] GUID.IsPermaLink = %q, want \"false\"", i, epItem.GUID.IsPermaLink)
		}
		if epItem.GUID.Value == "" {
			t.Errorf("episode item[%d] GUID.Value is empty", i)
		}
	}
}

func TestRSSXMLValid(t *testing.T) {
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 1, Name: "Test Movie", Year: "2020"},
	}
	rssItems := buildMovieRSSItems("http://localhost:7878", items)
	rss := buildRSS("http://localhost:7878", rssItems, 0, len(rssItems))

	data, err := xml.Marshal(rss)
	if err != nil {
		t.Fatalf("xml.Marshal: %v", err)
	}
	if !strings.Contains(string(data), "Test.Movie") {
		t.Errorf("marshalled XML missing movie title")
	}
}

func TestGUIDIsPermaLinkFalse(t *testing.T) {
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 42, Name: "Test Movie", Year: "2020"},
	}
	rssItems := buildMovieRSSItems("http://localhost:7878", items)
	if len(rssItems) == 0 {
		t.Fatal("expected at least one item")
	}
	guid := rssItems[0].GUID
	if guid.Value == "" {
		t.Error("GUID value should not be empty")
	}
	if guid.IsPermaLink != "false" {
		t.Errorf("GUID isPermaLink = %q, want \"false\"", guid.IsPermaLink)
	}

	// Verify it appears correctly in serialised XML
	data, err := xml.Marshal(buildRSS("http://localhost:7878", rssItems, 0, 1))
	if err != nil {
		t.Fatalf("xml.Marshal: %v", err)
	}
	if !strings.Contains(string(data), `isPermaLink="false"`) {
		t.Errorf("marshalled XML missing isPermaLink=\"false\": %s", string(data))
	}
}

func TestPubDateStable(t *testing.T) {
	item := &index.Item{
		Type:        index.TypeMovie,
		XtreamID:    1,
		Name:        "Stable Date Movie",
		ReleaseDate: "2021-06-15",
	}
	rssItems := buildMovieRSSItems("http://localhost:7878", []*index.Item{item})
	if len(rssItems) == 0 {
		t.Fatal("expected item")
	}
	date1 := rssItems[0].PubDate

	// Build again — date must be identical (not time.Now())
	rssItems2 := buildMovieRSSItems("http://localhost:7878", []*index.Item{item})
	date2 := rssItems2[0].PubDate

	if date1 != date2 {
		t.Errorf("PubDate is not stable: %q vs %q", date1, date2)
	}
	if strings.Contains(date1, "1970") {
		// If release date parses, we should not fall back to epoch
		t.Errorf("PubDate fell back to epoch for a known release date: %q", date1)
	}
}

func TestPubDateFallbackToEpoch(t *testing.T) {
	item := &index.Item{Type: index.TypeMovie, XtreamID: 1, Name: "No Date Movie"}
	rssItems := buildMovieRSSItems("http://localhost:7878", []*index.Item{item})
	if len(rssItems) == 0 {
		t.Fatal("expected item")
	}
	date1 := rssItems[0].PubDate
	if date1 == "" {
		t.Error("PubDate should not be empty")
	}

	// The fallback must be stable across calls — time.Now() would not be stable
	// and would cause Sonarr to re-grab every RSS sync.
	rssItems2 := buildMovieRSSItems("http://localhost:7878", []*index.Item{item})
	if date1 != rssItems2[0].PubDate {
		t.Errorf("fallback PubDate is not stable: %q vs %q", date1, rssItems2[0].PubDate)
	}

	// The fallback must not use the current year (it should be epoch: 1970).
	if !strings.Contains(date1, "1970") {
		t.Errorf("fallback PubDate should be Unix epoch (1970), got: %q", date1)
	}
}

func TestBuildTitle(t *testing.T) {
	cases := []struct {
		item *index.Item
		want string
	}{
		{
			&index.Item{Name: "Blade Runner 2049", Year: "2017", ContainerExt: "mkv"},
			"Blade.Runner.2049.2017.WEB-DL.mkv",
		},
		{
			&index.Item{Name: "No Year", Year: "", ContainerExt: "mp4"},
			"No.Year.WEB-DL.mp4",
		},
		// Year field is empty but ReleaseDate has a year — buildTitle must use it.
		// This covers the sync fix that populates Year from ReleaseDate for series.
		{
			&index.Item{Name: "Release Date Only", Year: "", ReleaseDate: "2019-03-22", ContainerExt: "mkv"},
			"Release.Date.Only.2019.WEB-DL.mkv",
		},
	}
	for _, c := range cases {
		got := buildTitle(c.item)
		if got != c.want {
			t.Errorf("buildTitle(%q year=%q releaseDate=%q) = %q, want %q",
				c.item.Name, c.item.Year, c.item.ReleaseDate, got, c.want)
		}
	}
}
