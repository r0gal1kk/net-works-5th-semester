package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

type Location struct {
	Name string
	Lat  float64
	Lon  float64
}

type Weather struct {
	Temp float64
	Desc string
}

type Place struct {
	Name string
	Xid  string
	Desc string
}

type Result struct {
	Location Location
	Weather  Weather
	Places   []Place
}

func fetchLocation(ctx context.Context, query string) (*Location, error) {
	graphhopperKey := os.Getenv("GRAPHHOPPER_KEY")
	q := url.QueryEscape(query)
	url := fmt.Sprintf("https://graphhopper.com/api/1/geocode?q=%s&key=%s", q, graphhopperKey)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := resp.Body.Close(); err != nil {
			err = cerr
		}
	}()

	var data struct {
		Hits []struct {
			Name  string `json:"name"`
			Point struct {
				Lat float64 `json:"lat"`
				Lon float64 `json:"lng"`
			} `json:"point"`
		} `json:"hits"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if len(data.Hits) == 0 {
		return nil, fmt.Errorf("no results found for %s", query)
	}

	fmt.Printf("Found %d results for '%s':\n", len(data.Hits), query)
	for i, hit := range data.Hits {
		location := hit.Name
		fmt.Printf("%d. %s (%.6f, %.6f)\n", i+1, location, hit.Point.Lat, hit.Point.Lon)
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("Select a location number: ")
		input, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		input = strings.TrimSpace(input)

		choice, err := strconv.Atoi(input)
		if err != nil || choice < 1 || choice > len(data.Hits) {
			fmt.Printf("Please enter a number between 1 and %d\n", len(data.Hits))
			continue
		}

		hit := data.Hits[choice-1]
		return &Location{Name: hit.Name, Lat: hit.Point.Lat, Lon: hit.Point.Lon}, nil
	}
}

func fetchWeather(ctx context.Context, lat, lon float64) (Weather, error) {
	apiKey := os.Getenv("OPENWEATHER_KEY")
	url := fmt.Sprintf("https://api.openweathermap.org/data/2.5/weather?lat=%f&lon=%f&units=metric&lang=ru&appid=%s", lat, lon, apiKey)

	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Weather{}, err
	}
	defer func() {
		if cerr := resp.Body.Close(); err != nil {
			err = cerr
		}
	}()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return Weather{}, err
	}
	var data struct {
		Weather []struct {
			Description string `json:"description"`
		} `json:"weather"`
		Main struct {
			Temp float64 `json:"temp"`
		} `json:"main"`
	}
	err = json.Unmarshal(body, &data)
	if err != nil {
		return Weather{}, err
	}

	desc := ""
	if len(data.Weather) > 0 {
		desc = data.Weather[0].Description
	}
	return Weather{Temp: data.Main.Temp, Desc: desc}, nil
}

func fetchPlacesAndDetails(ctx context.Context, lat, lon float64) ([]Place, error) {
	apiURL := fmt.Sprintf(
		"https://ru.wikipedia.org/w/api.php?action=query&list=geosearch&gscoord=%f|%f&gsradius=5000&gslimit=10&format=json",
		lat, lon,
	)

	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	req.Header.Set("User-Agent", "GoStudyApp/1.0 (your_email@example.com)")

	respCh := make(chan []Place)
	errCh := make(chan error)

	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- fmt.Errorf("ошибка при поиске в Wikipedia: %v", err)
			return
		}
		defer func() {
			if cerr := resp.Body.Close(); err != nil {
				err = cerr
			}
		}()

		body, _ := ioutil.ReadAll(resp.Body)
		var data struct {
			Query struct {
				Geosearch []struct {
					PageID int     `json:"pageid"`
					Title  string  `json:"title"`
					Lat    float64 `json:"lat"`
					Lon    float64 `json:"lon"`
				} `json:"geosearch"`
			} `json:"query"`
		}
		if err = json.Unmarshal(body, &data); err != nil {
			errCh <- fmt.Errorf("ошибка при разборе геопоиска: %v", err)
			return
		}
		if len(data.Query.Geosearch) == 0 {
			errCh <- fmt.Errorf("не найдено достопримечательностей поблизости")
			return
		}

		places := make([]Place, len(data.Query.Geosearch))
		var wg sync.WaitGroup
		for i, s := range data.Query.Geosearch {
			wg.Add(1)
			go func(i int, id int, title string) {
				defer wg.Done()
				desc, _ := fetchPlaceDetail(ctx, id)
				if len(desc) > 300 {
					desc = desc[:300] + "..."
				}
				places[i] = Place{Name: title, Desc: desc}
			}(i, s.PageID, s.Title)
		}
		wg.Wait()
		respCh <- places
	}()

	select {
	case places := <-respCh:
		return places, nil
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout при запросе достопримечательностей")
	}
}

func fetchPlaceDetail(ctx context.Context, pageID int) (string, error) {
	apiURL := fmt.Sprintf(
		"https://ru.wikipedia.org/w/api.php?action=query&pageids=%d&prop=extracts&exintro&explaintext&format=json",
		pageID,
	)

	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	req.Header.Set("User-Agent", "GoStudyApp/1.0 (your_email@example.com)")

	descCh := make(chan string)
	errCh := make(chan error)

	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- fmt.Errorf("ошибка при запросе описания Wikipedia: %v", err)
			return
		}
		defer func() {
			if cerr := resp.Body.Close(); err != nil {
				err = cerr
			}
		}()

		body, _ := ioutil.ReadAll(resp.Body)
		var result struct {
			Query struct {
				Pages map[string]struct {
					Extract string `json:"extract"`
				} `json:"pages"`
			} `json:"query"`
		}
		if err = json.Unmarshal(body, &result); err != nil {
			errCh <- err
			return
		}

		for _, page := range result.Query.Pages {
			if page.Extract != "" {
				descCh <- page.Extract
				return
			}
		}
		descCh <- "Описание отсутствует"
	}()

	select {
	case desc := <-descCh:
		return desc, nil
	case err := <-errCh:
		return "", err
	case <-ctx.Done():
		return "", fmt.Errorf("timeout при запросе описания места")
	}
}

func fetchAll(ctx context.Context, city string) (Result, error) {
	resultCh := make(chan Result)
	errCh := make(chan error)

	go func() {
		loc, err := fetchLocation(ctx, city)
		if err != nil {
			errCh <- err
			return
		}

		var wg sync.WaitGroup
		var weather Weather
		var places []Place
		var weatherErr, placesErr error

		wg.Add(2)

		go func() {
			defer wg.Done()
			weather, weatherErr = fetchWeather(ctx, loc.Lat, loc.Lon)
		}()

		go func() {
			defer wg.Done()
			places, placesErr = fetchPlacesAndDetails(ctx, loc.Lat, loc.Lon)
		}()

		wg.Wait()

		if weatherErr != nil {
			errCh <- weatherErr
			return
		}
		if placesErr != nil {
			errCh <- placesErr
			return
		}

		resultCh <- Result{
			Location: *loc,
			Weather:  weather,
			Places:   places,
		}
	}()

	select {
	case res := <-resultCh:
		return res, nil
	case err := <-errCh:
		return Result{}, err
	case <-ctx.Done():
		return Result{}, fmt.Errorf("timeout при загрузке данных")
	}
}

func main() {
	err := godotenv.Load()
	if err != nil {
		fmt.Println("Не удалось загрузить .env файл:", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Введите место: ")
	query, _ := reader.ReadString('\n')
	query = strings.TrimSpace(query)

	result, err := fetchAll(ctx, query)
	if err != nil {
		fmt.Println("Ошибка:", err)
		return
	}

	fmt.Printf("\n--- Результат для %s ---\n", result.Location.Name)
	fmt.Printf("Погода: %.1f°C, %s\n", result.Weather.Temp, result.Weather.Desc)
	fmt.Println("\nИнтересные места:")
	for _, p := range result.Places {
		if p.Name != "" {
			fmt.Printf("- %s: %s\n", p.Name, p.Desc)
		}
	}
}
