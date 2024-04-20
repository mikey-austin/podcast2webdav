package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"strconv"

	log "github.com/sirupsen/logrus"

	"github.com/mmcdole/gofeed"
	"github.com/spf13/pflag"
	"golang.org/x/net/webdav"
	"gopkg.in/yaml.v2"
)

type Config struct {
	AppName string `yaml:"appName"`
	Feeds   []struct {
		Name string `yaml:"name"`
		URL  string `yaml:"url"`
	} `yaml:"feeds"`
}

func parseConfig(path string) (*Config, error) {
	file, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var config Config
	err = yaml.Unmarshal(file, &config)
	if err != nil {
		return nil, err
	}
	return &config, nil
}

func fetchFeeds(config *Config) ([]*gofeed.Feed, error) {
	parser := gofeed.NewParser()
	var feeds []*gofeed.Feed
	for _, feedConfig := range config.Feeds {
		feed, err := parser.ParseURL(feedConfig.URL)
		if err != nil {
			log.Printf("Failed to fetch feed: %s", feedConfig.Name)
			continue
		}
		feeds = append(feeds, feed)
	}
	log.WithField("Feeds", len(feeds)).Info("re-loaded podcast feeds")
	return feeds, nil
}

func main() {
	configFile := pflag.String("config", "podcast2rygel.yaml", "the main config file containing the feeds")
	verbose := pflag.Bool("verbose", false, "enables more debug information")
	pflag.Parse()

	if *verbose {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	config, err := parseConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to parse config: %v", err)
	}
	log.WithField("config", config).WithField("file", configFile).Debug("parsed configuration file")

	// Create an in-memory filesystem
	fs := webdav.NewMemFS()

	// Populate the memfs with audio files (fetch on demand)
	feeds, err := fetchFeeds(config)
	if err != nil {
		log.Fatalf("Failed to fetch RSS feeds: %v", err)
	}

	context := context.Background()
	episodeUrlMap := make(map[string]*gofeed.Item)
	fs.Mkdir(context, "/podcasts", os.ModePerm)
	for i, feedConfig := range config.Feeds {
		feedBasePath := "/podcasts/" + feedConfig.Name
		fs.Mkdir(context, feedBasePath, os.ModePerm)
		for j, item := range feeds[i].Items {
			episodePath := feedBasePath + "/" + makeEpisodeName(len(feeds[i].Items) - j, item)
			episodeFile, err := fs.OpenFile(context, episodePath, os.O_CREATE|os.O_RDWR, 0644)
			if err != nil {
				log.Fatalf("Failed to create episode path: "+episodePath, err)
			}
			episodeFile.Write([]byte{})
			episodeUrlMap[episodePath] = item
			episodeFile.Close()
		}
	}

	// Create a WebDAV handler
	handler := &webdav.Handler{
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Handle WebDAV requests
		switch r.Method {
		case http.MethodGet:
			// Serve audio files dynamically
			webdavFilePath := r.URL.Path
			if strings.HasPrefix(webdavFilePath, "/podcasts/") {
				episode := episodeUrlMap[webdavFilePath]
				if episode == nil {
					return
				}
				resp, err := http.Get(episode.Enclosures[0].URL)
				if err != nil {
					http.Error(w, "Error fetching audio content", http.StatusInternalServerError)
					return
				}
				defer resp.Body.Close()

				// Set appropriate headers for audio file
				w.Header().Set("Content-Type", episode.Enclosures[0].Type)
				w.Header().Set("Content-Length", fmt.Sprintf("%d", resp.ContentLength))

				// Stream audio content directly to the response
				_, _ = io.Copy(w, resp.Body)
				return
			}
		}

		// Serve WebDAV requests
		handler.ServeHTTP(w, r)
	})

	port := 8080
	fmt.Printf("WebDAV server listening on port %d...\n", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

func makeEpisodeName(index int, item *gofeed.Item) string {
	url := item.Enclosures[0].URL
	byDot := strings.Split(url, ".")
	date := item.PublishedParsed.Format(time.DateOnly)
	return date + "_episode" + strconv.Itoa(index) + "." + byDot[len(byDot)-1]

}
