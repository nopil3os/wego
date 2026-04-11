package frontends

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"time"

	colorable "github.com/mattn/go-colorable"
	runewidth "github.com/mattn/go-runewidth"
	"github.com/schachmat/wego/iface"
)

type mdConfig struct {
	coords bool
	unit   iface.UnitSystem
}

func mdPad(s string, mustLen int) (ret string) {
	realLen := runewidth.StringWidth(s)
	if realLen == mustLen {
		return s
	}
	if realLen < mustLen {
		return s + strings.Repeat(" ", mustLen-realLen)
	}
	// realLen > mustLen, truncate
	return runewidth.Truncate(s, mustLen, "") // Truncate without an ellipsis
}

func (c *mdConfig) formatTemp(cond iface.Cond) string {
	cvtUnits := func(temp float32) string {
		t, _ := c.unit.Temp(temp)
		return fmt.Sprintf("%d", int(t))
	}
	_, u := c.unit.Temp(0.0)

	if cond.TempC == nil {
		return fmt.Sprintf("? %s", u)
	}

	t := *cond.TempC
	if cond.FeelsLikeC != nil {
		fl := *cond.FeelsLikeC
		return fmt.Sprintf("%s (%s) %s", cvtUnits(t), cvtUnits(fl), u)
	}
	return fmt.Sprintf("%s %s", cvtUnits(t), u)
}

func (c *mdConfig) formatWind(cond iface.Cond) string {
	windDir := func(deg *int) string {
		if deg == nil {
			return "?"
		}
		arrows := []string{"↓", "↙", "←", "↖", "↑", "↗", "→", "↘"}
		return arrows[((*deg+22)%360)/45]
	}
	spdStr := func(spdKmph float32) string {
		s, _ := c.unit.Speed(spdKmph)
		return fmt.Sprintf("%d", int(s))
	}

	_, u := c.unit.Speed(0.0)

	if cond.WindspeedKmph == nil {
		return windDir(cond.WinddirDegree)
	}
	s := *cond.WindspeedKmph

	if cond.WindGustKmph != nil {
		if g := *cond.WindGustKmph; g > s {
			return fmt.Sprintf("%s %s – %s %s", windDir(cond.WinddirDegree), spdStr(s), spdStr(g), u)
		}
	}

	return fmt.Sprintf("%s %s %s", windDir(cond.WinddirDegree), spdStr(s), u)
}

func (c *mdConfig) formatVisibility(cond iface.Cond) string {
	if cond.VisibleDistM == nil {
		return ""
	}
	v, u := c.unit.Distance(*cond.VisibleDistM)
	return fmt.Sprintf("%d %s", int(v), u)
}

func (c *mdConfig) formatRain(cond iface.Cond) string {
	if cond.PrecipM != nil {
		v, u := c.unit.Distance(*cond.PrecipM)
		u += "/h" // it's the same in all unit systems
		if cond.ChanceOfRainPercent != nil {
			return fmt.Sprintf("%.1f %s %d%%", v, u, *cond.ChanceOfRainPercent)
		}
		return fmt.Sprintf("%.1f %s", v, u)
	} else if cond.ChanceOfRainPercent != nil {
		return fmt.Sprintf("%d%%", *cond.ChanceOfRainPercent)
	}
	return ""
}

func (c *mdConfig) formatCond(cond iface.Cond, current bool) (contentStrings []string, icon string) {
	codes := map[iface.WeatherCode]string{
		iface.CodeUnknown:             "✨",
		iface.CodeCloudy:              "☁️",
		iface.CodeFog:                 "🌫",
		iface.CodeHeavyRain:           "🌧",
		iface.CodeHeavyShowers:        "🌧",
		iface.CodeHeavySnow:           "❄️",
		iface.CodeHeavySnowShowers:    "❄️",
		iface.CodeLightRain:           "🌦",
		iface.CodeLightShowers:        "🌦",
		iface.CodeLightSleet:          "🌧",
		iface.CodeLightSleetShowers:   "🌧",
		iface.CodeLightSnow:           "🌨",
		iface.CodeLightSnowShowers:    "🌨",
		iface.CodePartlyCloudy:        "⛅️",
		iface.CodeSunny:               "☀️",
		iface.CodeThunderyHeavyRain:   "🌩",
		iface.CodeThunderyShowers:     "⛈",
		iface.CodeThunderySnowShowers: "⛈",
		iface.CodeVeryCloudy:          "☁️",
	}

	var ok bool
	icon, ok = codes[cond.Code]
	if !ok {
		log.Fatalln("markdown-frontend: The following weather code has no icon:", cond.Code)
	}

	desc := cond.Desc
	if !current {
		// Truncate description for forecast, current description is not truncated
		desc = runewidth.Truncate(runewidth.FillRight(desc, 25), 25, "…")
	}

	contentStrings = make([]string, 5)
	contentStrings[0] = desc
	contentStrings[1] = c.formatTemp(cond)
	contentStrings[2] = c.formatWind(cond)
	contentStrings[3] = c.formatVisibility(cond)
	contentStrings[4] = c.formatRain(cond)

	return contentStrings, icon
}

func (c *mdConfig) formatGeo(coords *iface.LatLon) (ret string) {
	if !c.coords || coords == nil {
		return ""
	}

	lat, lon := "N", "E"
	if coords.Latitude < 0 {
		lat = "S"
	}
	if coords.Longitude < 0 {
		lon = "W"
	}
	ret = " "
	ret += fmt.Sprintf("(%.1f°%s", math.Abs(float64(coords.Latitude)), lat)
	ret += fmt.Sprintf("%.1f°%s)", math.Abs(float64(coords.Longitude)), lon)
	return
}

func (c *mdConfig) printDay(day iface.Day) (ret []string) {
	desiredTimesOfDay := []time.Duration{
		8 * time.Hour,
		12 * time.Hour,
		19 * time.Hour,
		23 * time.Hour,
	}

	rows := make([]string, 5)
	for i := range rows {
		rows[i] = "|" // Start each row with a separator
	}

	cols := make([]iface.Cond, len(desiredTimesOfDay))
	for _, candidate := range day.Slots {
		cand := candidate.Time.UTC().Sub(candidate.Time.Truncate(24 * time.Hour))
		for i, col := range cols {
			cur := col.Time.Sub(col.Time.Truncate(24 * time.Hour))
			if col.Time.IsZero() || math.Abs(float64(cand-desiredTimesOfDay[i])) < math.Abs(float64(cur-desiredTimesOfDay[i])) {
				cols[i] = candidate
			}
		}
	}

	for _, s := range cols {
		// Get 5 unpadded content strings and the icon for the current column
		contentStrings, icon := c.formatCond(s, false)

		// Construct and pad each line for this column, then append to rows
		rows[0] += mdPad(fmt.Sprintf(" %s ", contentStrings[0]), 25) + "|"
		rows[1] += mdPad(fmt.Sprintf(" %s %s ", icon, contentStrings[1]), 25) + "|"
		rows[2] += mdPad(fmt.Sprintf(" 🌬️ %s ", contentStrings[2]), 25) + "|"
		rows[3] += mdPad(fmt.Sprintf(" 👁️ %s ", contentStrings[3]), 25) + "|"
		rows[4] += mdPad(fmt.Sprintf(" 💦  %s ", contentStrings[4]), 26) + "|"
	}
	dateFmt := day.Date.Format("Mon Jan 02")
	ret = append([]string{
		"\n### Forecast for " + dateFmt + "\n",
		fmt.Sprintf("|%s|%s|%s|%s|",
			mdPad(" Morning ", 25),
			mdPad(" Noon ", 25),
			mdPad(" Evening ", 25),
			mdPad(" Night ", 25)),
		// Lines are shorter to account for spaces as this isn't using mdPad
		fmt.Sprintf("| %s | %s | %s | %s |",
			strings.Repeat("-", 23),
			strings.Repeat("-", 23),
			strings.Repeat("-", 23),
			strings.Repeat("-", 23))},
		rows...)
	return ret
}

func (c *mdConfig) Setup() {
	flag.BoolVar(&c.coords, "md-coords", false, "md-frontend: Show geo coordinates")
}

func (c *mdConfig) Render(r iface.Data, unitSystem iface.UnitSystem) {
	c.unit = unitSystem
	_, _ = fmt.Printf("## Weather for %s%s\n\n", r.Location, c.formatGeo(r.GeoLoc))
	stdout := colorable.NewNonColorable(os.Stdout)

	// Get unpadded content strings and icon for current conditions
	contentStrings, icon := c.formatCond(r.Current, true)

	// Print current conditions with icons and leading space
	_, _ = fmt.Fprintln(stdout, " "+mdPad(contentStrings[0], 25))                             // Description
	_, _ = fmt.Fprintln(stdout, " "+mdPad(fmt.Sprintf("%s %s", icon, contentStrings[1]), 25)) // Temp
	_, _ = fmt.Fprintln(stdout, " "+mdPad(fmt.Sprintf("🌬️ %s", contentStrings[2]), 25))       // Wind
	_, _ = fmt.Fprintln(stdout, " "+mdPad(fmt.Sprintf("👁️ %s", contentStrings[3]), 25))       // Visibility
	_, _ = fmt.Fprintln(stdout, " "+mdPad(fmt.Sprintf("💧 %s", contentStrings[4]), 25))        // Rain

	if len(r.Forecast) == 0 {
		return
	}
	if r.Forecast == nil {
		log.Fatal("No detailed weather forecast available.")
	}
	for _, d := range r.Forecast {
		for _, val := range c.printDay(d) {
			_, _ = fmt.Fprintln(stdout, val)
		}
	}
}

func init() {
	iface.AllFrontends["markdown"] = &mdConfig{}
}
