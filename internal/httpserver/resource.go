package httpserver

import (
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
)

func staticHandler(root string, fallbackIndex bool) http.Handler {
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/"))
		if path == "" {
			path = "."
		}
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			if fallbackIndex {
				http.ServeFile(w, r, filepath.Join(root, "index.html"))
				return
			}
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func spaHandler(root string) http.Handler {
	return staticHandler(root, true)
}

func placeholder(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/bangumi/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	savePath := html.EscapeString(config.Cfg.SavePath)
	frontPath := html.EscapeString(config.Cfg.FrontStaticPath)
	fmt.Fprintf(w, `<h1>uani HTTP Service</h1>
<pre>Please modify your web server configure file
to server this path to '%s'.
e.g.

...
autoindex on;
location / {
    alias %s/;
}
location /bangumi {
    alias %s/;
}
...

If use want main to serve static files, please run this command and <strong>restart main</strong>

<code>uani config set http serve_static_files --value true</code></pre>`, savePath, frontPath, savePath)
}

func calendarICS(w http.ResponseWriter, r *http.Request) {
	if err := ensureDB(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type entry struct {
		name      string
		updateDay string
		status    int
	}
	var data []entry
	list, err := db.Conn.AllFollowedBangumi(db.BangumiStatusUpdating)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, item := range list {
		data = append(data, entry{name: item.Bangumi.Name, updateDay: item.Bangumi.UpdateDay, status: item.Followed.Status})
	}
	scripts, _ := db.Conn.AllScripts()
	for _, item := range scripts {
		data = append(data, entry{name: item.BangumiName, updateDay: item.UpdateDay, status: item.Status})
	}

	today := time.Now()
	var lines []string
	lines = append(lines,
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//uani Followed Bangumi Calendar//uani//",
	)

	if _, typeProvided := r.URL.Query()["type"]; !typeProvided {
		byWeekday := map[int][]string{}
		for _, item := range data {
			if weekday, ok := dayToWeekday(item.updateDay); ok {
				byWeekday[weekday] = append(byWeekday[weekday], item.name)
			}
		}
		todayWeekday := (int(today.Weekday()) + 6) % 7
		for i := 0; i < 7; i++ {
			weekday := (todayWeekday + i) % 7
			for _, name := range byWeekday[weekday] {
				date := today.AddDate(0, 0, i).Format("20060102")
				lines = append(lines, "BEGIN:VEVENT", "SUMMARY:"+escapeICS(name), "DTSTART;VALUE=DATE:"+date, "DTEND;VALUE=DATE:"+date, "END:VEVENT")
			}
		}
	} else {
		date := today.Format("20060102")
		for _, item := range data {
			if item.status != db.FollowedStatusUpdated {
				continue
			}
			lines = append(lines, "BEGIN:VEVENT", "SUMMARY:"+escapeICS("Updated: "+item.name), "DTSTART;VALUE=DATE:"+date, "DTEND;VALUE=DATE:"+date, "END:VEVENT")
		}
	}

	lines = append(lines,
		"NAME:Bangumi Calendar",
		"X-WR-CALNAME:Bangumi Calendar",
		"DESCRIPTION:Followed Bangumi Calendar",
		"X-WR-CALDESC:Followed Bangumi Calendar",
		"END:VCALENDAR",
	)
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	fmt.Fprint(w, strings.Join(lines, "\r\n")+"\r\n")
}

func dayToWeekday(day string) (int, bool) {
	switch day {
	case "Mon":
		return 0, true
	case "Tue":
		return 1, true
	case "Wed":
		return 2, true
	case "Thu":
		return 3, true
	case "Fri":
		return 4, true
	case "Sat":
		return 5, true
	case "Sun":
		return 6, true
	default:
		return 0, false
	}
}

func escapeICS(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		";", "\\;",
		",", "\\,",
		"\n", "\\n",
		"\r", "",
	)
	return replacer.Replace(value)
}
