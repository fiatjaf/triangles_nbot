package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/fogleman/primitive/primitive"
	"github.com/kelseyhightower/envconfig"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/nfnt/resize"
)

const (
	INPUT_FILENAME  = "triangles-nbot-in.png"
	OUTPUT_FILENAME = "triangles-nbot-out.png"
	RELAY_URL       = "wss://nostr.wine"
)

var s Settings

type Settings struct {
	SecretKey        string `envconfig:"SECRET_KEY"`
	UnsplashClientID string `envconfig:"UNSPLASH_CLIENT_ID"`
}

func main() {
	if err := envconfig.Process("", &s); err != nil {
		log.Fatalf("failed to read from env: %s", err)
		return
	}

	// get random picture from unsplash
	resp, err := http.Get("https://api.unsplash.com/photos/random?client_id=" + s.UnsplashClientID + "&topics=nature,cathedral,outdoors,landscape,cafe,restaurante")
	if err != nil {
		log.Fatalf("unsplash call failed: %s", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		log.Fatalf("unsplash read failed: %s", data)
	}

	var unsp struct {
		ID   string `json:"id"`
		URLs struct {
			Regular string `json:"regular"`
		} `json:"urls"`
		Links struct {
			HTML string `json:"html"`
		} `json:"links"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&unsp); err != nil {
		log.Fatalf("unsplash decode failed: %s", err)
	}

	// prepare files (this is not really necessary, we should just load stuff from memory)
	inputpath := filepath.Join(os.TempDir(), INPUT_FILENAME)
	outputpath := filepath.Join(os.TempDir(), OUTPUT_FILENAME)
	defer os.RemoveAll(inputpath)
	defer os.RemoveAll(outputpath)

	// download file
	resp, err = http.Get(unsp.URLs.Regular)
	if err != nil {
		log.Fatalf("failed to download picture: %s", err)
		return
	}
	defer resp.Body.Close()
	file, err := os.Create(inputpath)
	if err != nil {
		log.Fatalf("failed to create file: %s", err)
		return
	}
	defer file.Close()
	if _, err := io.Copy(file, resp.Body); err != nil {
		log.Fatalf("failed to save picture: %s", err)
		return
	}

	// generate primitive image
	rand.Seed(time.Now().UTC().UnixNano())
	input, err := primitive.LoadImage(inputpath)
	if _, err := io.Copy(file, resp.Body); err != nil {
		log.Fatalf("failed to create primitive: %s", err)
		return
	}
	size := uint(256)
	if size > 0 {
		input = resize.Thumbnail(size, size, input, resize.Bilinear)
	}
	bg := primitive.MakeColor(primitive.AverageImageColor(input))
	model := primitive.NewModel(input, bg, 1024, 1)
	for i := 0; i < 225; i++ {
		model.Step(primitive.ShapeTypeTriangle, 128, 0)
	}
	if err := primitive.SavePNG(outputpath, model.Context.Image()); err != nil {
		log.Fatalf("failed to save primitive png: %s", err)
		return
	}

	// publish to satellite
	uploadEvent := nostr.Event{
		CreatedAt: nostr.Now(),
		Kind:      22242,
		Content:   "Authorize Upload",
		Tags: nostr.Tags{
			nostr.Tag{"name", "unsplash-" + unsp.ID},
		},
	}
	if err := uploadEvent.Sign(s.SecretKey); err != nil {
		log.Fatalf("failed to sign upload: %s", err)
		return
	}

	u, _ := url.Parse("https://api.satellite.earth/v1/media/item")
	qs := u.Query()
	qs.Add("auth", uploadEvent.String())
	u.RawQuery = qs.Encode()

	file, err = os.Open(outputpath)
	if err != nil {
		log.Fatalf("failed to open file: %s", err)
		return
	}
	defer file.Close()

	req, _ := http.NewRequest("PUT", u.String(), file)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("failed to upload: %s", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		log.Fatalf("failed to upload: %s", string(data))
		return
	}

	var image struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&image); err != nil {
		log.Fatalf("failed to decode response from satellite: %s", err)
		return
	}

	// publish nostr event
	event := nostr.Event{
		CreatedAt: nostr.Now(),
		Kind:      1,
		Content:   fmt.Sprintf("%s \n#triangles %s", image.URL, unsp.Links.HTML),
		Tags: nostr.Tags{
			nostr.Tag{"t", "triangles"},
		},
	}
	event.Sign(s.SecretKey)

	relay, err := nostr.RelayConnect(context.Background(), RELAY_URL)
	if err != nil {
		log.Fatalf("failed to connect: %s", err)
		return
	}

	if _, err := relay.Publish(context.Background(), event); err != nil {
		log.Fatalf("failed to publish: %s", err)
		return
	}

	fmt.Println(event)

	nevent, _ := nip19.EncodeEvent(event.ID, []string{RELAY_URL}, "")
	fmt.Println("https://nostr.com/" + nevent)
}
