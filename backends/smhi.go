package backends

import (
	"encoding/json"
	"fmt"
	"github.com/schachmat/wego/iface"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type smhiConfig struct {
}

type smhiData struct {
	AirTemperature          float64 `json:"air_temperature"`
	WindFromDirection       float64 `json:"wind_from_direction"`
	WindSpeed               float64 `json:"wind_speed"`
	WindSpeedOfGust         float64 `json:"wind_speed_of_gust"`
	RelativeHumidity        float64 `json:"relative_humidity"`
	VisibilityInAir         float64 `json:"visibility_in_air"`
	PrecipitationAmountMean float64 `json:"precipitation_amount_mean"`
	SymbolCode              float64 `json:"symbol_code"`
}

type smhiTimeSeries struct {
	Time                        string   `json:"time"`
	IntervalParametersStartTime string   `json:"intervalParametersStartTime"`
	Data                        smhiData `json:"data"`
}

type smhiGeometry struct {
	Coordinates []float32 `json:"coordinates"`
}

type smhiResponse struct {
	CreatedTime   string            `json:"createdTime"`
	ReferenceTime string            `json:"referenceTime"`
	Geometry      smhiGeometry      `json:"geometry"`
	TimeSeries    []*smhiTimeSeries `json:"timeSeries"`
}

type smhiCondition struct {
	WeatherCode iface.WeatherCode
	Description string
}

const (
	// API Spec: https://opendata.smhi.se/metfcst/snow1gv1/parameters
	smhiWuri = "https://opendata-download-metfcst.smhi.se/api/category/snow1g/version/1/geotype/point/lon/%s/lat/%s/data.json"
)

var (
	weatherConditions = map[int]smhiCondition{
		1:  {iface.CodeSunny, "Clear Sky"},
		2:  {iface.CodeSunny, "Nearly Clear Sky"},
		3:  {iface.CodePartlyCloudy, "Variable cloudiness"},
		4:  {iface.CodePartlyCloudy, "Halfclear sky"},
		5:  {iface.CodeCloudy, "Cloudy sky"},
		6:  {iface.CodeVeryCloudy, "Overcast"},
		7:  {iface.CodeFog, "Fog"},
		8:  {iface.CodeLightShowers, "Light rain showers"},
		9:  {iface.CodeLightShowers, "Moderate rain showers"},
		10: {iface.CodeHeavyShowers, "Heavy rain showers"},
		11: {iface.CodeThunderyShowers, "Thunderstorm"},
		12: {iface.CodeLightSleetShowers, "Light sleet showers"},
		13: {iface.CodeLightSleetShowers, "Moderate sleet showers"},
		14: {iface.CodeHeavySnowShowers, "Heavy sleet showers"},
		15: {iface.CodeLightSnowShowers, "Light snow showers"},
		16: {iface.CodeLightSnowShowers, "Moderate snow showers"},
		17: {iface.CodeHeavySnowShowers, "Heavy snow showers"},
		18: {iface.CodeLightRain, "Light rain"},
		19: {iface.CodeLightRain, "Moderate rain"},
		20: {iface.CodeHeavyRain, "Heavy rain"},
		21: {iface.CodeThunderyHeavyRain, "Thunder"},
		22: {iface.CodeLightSleet, "Light sleet"},
		23: {iface.CodeLightSleet, "Moderate sleet"},
		24: {iface.CodeHeavySnow, "Heavy sleet"},
		25: {iface.CodeLightSnow, "Light snowfall"},
		26: {iface.CodeLightSnow, "Moderate snowfall"},
		27: {iface.CodeHeavySnow, "Heavy snowfall"},
	}
)

func (c *smhiConfig) Setup() {
}

func (c *smhiConfig) fetch(url string) (*smhiResponse, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("Unable to get (%s): %v", url, err)
	} else if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		quip := ""
		if string(body) == "Requested point is out of bounds" {
			quip = "\nPlease note that SMHI only service the nordic countries."
		}
		return nil, fmt.Errorf("Unable to get (%s): http status %d, %s%s", url, resp.StatusCode, body, quip)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Unable to read response body (%s): %v", url, err)
	}

	var response smhiResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, fmt.Errorf("Unable to parse response (%s): %v", url, err)
	}
	return &response, nil
}

func (c *smhiConfig) Fetch(location string, numDays int) (ret iface.Data) {
	if matched, err := regexp.MatchString(`^-?[0-9]*(\.[0-9]+)?,-?[0-9]*(\.[0-9]+)?$`, location); !matched || err != nil {
		log.Fatalf("Error: The smhi backend only supports latitude,longitude pairs as location.\nInstead of `%s` try `59.329,18.068` for example to get a forecast for Stockholm.", location)
	}

	s := strings.Split(location, ",")
	requestUrl := fmt.Sprintf(smhiWuri, s[1], s[0])

	resp, err := c.fetch(requestUrl)
	if err != nil {
		log.Fatalf("Failed to fetch weather data: %v\n", err)
	}

	ret.Current = c.parseCurrent(resp)
	ret.Forecast = c.parseForecast(resp, numDays)
	coordinates := resp.Geometry.Coordinates
	ret.GeoLoc = &iface.LatLon{Latitude: coordinates[1], Longitude: coordinates[0]}
	ret.Location = location + " (Forecast provided by SMHI)"
	return ret
}

func (c *smhiConfig) parseForecast(response *smhiResponse, numDays int) (days []iface.Day) {
	if numDays > 10 {
		numDays = 10
	}

	var currentTime time.Time = time.Now()
	var dayCount = 0

	var day iface.Day
	day.Date = time.Now()
	for _, prediction := range response.TimeSeries {
		if dayCount == numDays {
			break
		}

		ts, err := time.Parse(time.RFC3339, prediction.Time)
		if err != nil {
			log.Fatalf("Failed to parse timestamp: %v\n", err)
		}

		if ts.Day() != currentTime.Day() {
			dayCount += 1
			currentTime = ts
			days = append(days, day)
			day = iface.Day{Date: ts}
		}
		day.Slots = append(day.Slots, c.parsePrediction(prediction))
	}

	return days
}

func (c *smhiConfig) parseCurrent(forecast *smhiResponse) (cnd iface.Cond) {
	if len(forecast.TimeSeries) == 0 {
		log.Fatalln("Failed to fetch weather data: No Forecast in response")
	}
	var currentPrediction *smhiTimeSeries = forecast.TimeSeries[0]
	var currentTime time.Time = time.Now().UTC()

	for _, prediction := range forecast.TimeSeries {
		ts, err := time.Parse(time.RFC3339, prediction.Time)
		if err != nil {
			log.Fatalf("Failed to parse timestamp: %v\n", err)
		}

		if ts.After(currentTime) {
			break
		}
	}
	return c.parsePrediction(currentPrediction)
}

func (c *smhiConfig) parsePrediction(prediction *smhiTimeSeries) (cnd iface.Cond) {
	ts, err := time.Parse(time.RFC3339, prediction.Time)
	if err != nil {
		log.Fatalf("Failed to parse timestamp: %v\n", err)
	}
	cnd.Time = ts

	precip := float32(prediction.Data.PrecipitationAmountMean / 1000) // Convert mm/h to m/h
	cnd.PrecipM = &precip

	vis := float32(prediction.Data.VisibilityInAir * 1000) // Convert km to m
	cnd.VisibleDistM = &vis

	temp := float32(prediction.Data.AirTemperature)
	cnd.TempC = &temp

	symbolCode := int(prediction.Data.SymbolCode)
	if condition, ok := weatherConditions[symbolCode]; ok {
		cnd.Code = condition.WeatherCode
		cnd.Desc = condition.Description
	} else {
		cnd.Code = iface.CodeUnknown
		cnd.Desc = fmt.Sprintf("Unknown weather condition (symbol code %d)", symbolCode)
	}

	windSpeed := float32(prediction.Data.WindSpeed * 3.6) // convert m/s to km/h
	cnd.WindspeedKmph = &windSpeed

	gustSpeed := float32(prediction.Data.WindSpeedOfGust * 3.6) // convert m/s to km/h
	cnd.WindGustKmph = &gustSpeed

	wd := int(prediction.Data.WindFromDirection)
	cnd.WinddirDegree = &wd

	humidity := int(prediction.Data.RelativeHumidity)
	cnd.Humidity = &humidity

	return cnd
}

func init() {
	iface.AllBackends["smhi"] = &smhiConfig{}
}
