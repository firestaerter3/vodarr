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
	rss := buildRSS("http://localhost:7878", items, 0, 1)

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
	series := &index.Item{Type: index.TypeSeries, XtreamID: 2, Name: "Series"}

	rssMovie := buildRSS("http://localhost", []*index.Item{movie}, 0, 1)
	rssSeries := buildRSS("http://localhost", []*index.Item{series}, 0, 1)

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

func TestRSSXMLValid(t *testing.T) {
	items := []*index.Item{
		{Type: index.TypeMovie, XtreamID: 1, Name: "Test Movie", Year: "2020"},
	}
	rss := buildRSS("http://localhost:7878", items, 0, 1)

	data, err := xml.Marshal(rss)
	if err != nil {
		t.Fatalf("xml.Marshal: %v", err)
	}
	if !strings.Contains(string(data), "Test.Movie") {
		t.Errorf("marshalled XML missing movie title")
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
	}
	for _, c := range cases {
		got := buildTitle(c.item)
		if got != c.want {
			t.Errorf("buildTitle(%q) = %q, want %q", c.item.Name, got, c.want)
		}
	}
}
