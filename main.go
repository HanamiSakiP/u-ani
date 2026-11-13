package uani

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/controllers"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/download"
	"github.com/HanamiSakiP/u-ani/internal/httpserver"
	"github.com/HanamiSakiP/u-ani/internal/kazumi"
	"github.com/HanamiSakiP/u-ani/internal/logger"
	"github.com/HanamiSakiP/u-ani/internal/model"
	"github.com/HanamiSakiP/u-ani/internal/postprocessor"
	"github.com/HanamiSakiP/u-ani/internal/script"
	"github.com/HanamiSakiP/u-ani/internal/source"
	"github.com/HanamiSakiP/u-ani/internal/update"
	"github.com/HanamiSakiP/u-ani/internal/updatecheck"
	"github.com/HanamiSakiP/u-ani/internal/utils"
)

var updateTimeOrder = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Unknown"}

var spacialAppendChars = []string{"Ⅱ", "Ⅲ", "♪", "Δ", "×", "☆", "É", "·", "♭", "★"}

func ensureDB() error {
	if db.Conn != nil {
		return nil
	}
	_, err := db.New()
	return err
}

func printResult(result controllers.Result) {
	switch result.Status {
	case "success":
		logger.Success("%s", result.Message)
	case "warning":
		logger.Warning("%s", result.Message)
	default:
		logger.Error("%s", result.Message)
		if result.Status == "error" {
			os.Exit(1)
		}
	}
}

func optString(cmd *cobra.Command, name string) *string {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	value, _ := cmd.Flags().GetString(name)
	return &value
}

func optInt(cmd *cobra.Command, name string) *int {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	value, _ := cmd.Flags().GetInt(name)
	return &value
}

func pythonStringList(values []string) string {
	if values == nil {
		return "None"
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = "'" + v + "'"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func pythonIntList(values []int) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprint(v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

var rootCmd = &cobra.Command{
	Use:           "uani",
	Short:         "uani - anime bangumi downloader",
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       config.Version,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if changed, err := cmd.Root().Flags().GetBool("version"); err == nil && changed {
			return
		}
		switch cmd.Name() {
		case "install", "upgrade", "completion", "http":
			return
		}
		updatecheck.CheckUpdate()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install uani data directories and database.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := update.CreateDir(); err != nil {
			return err
		}
		if err := update.InitDB(); err != nil {
			return err
		}
		update.InstallCrontab()
		if err := config.WriteDefaultConfig(); err != nil {
			return err
		}
		update.UpdateDatabase()
		noWeb, _ := cmd.Flags().GetBool("no-web")
		if !noWeb {
			logger.Info("built-in web UI is bundled; no external frontend download is needed")
		}
		return nil
	},
}

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade from previous version.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := update.CreateDir(); err != nil {
			return err
		}
		update.UpdateDatabase()
		updatecheck.CheckUpdate()
		return nil
	},
}

var sourceCmd = &cobra.Command{
	Use:   "source <source>",
	Short: "Select data source bangumi_moe or mikan_project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := args[0]
		valid := false
		for _, site := range config.SupportWebsite {
			if site.ID == target {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("please check your input. data source should be one of [bangumi_moe mikan_project dmhy]")
		}
		if err := ensureDB(); err != nil {
			return err
		}
		if err := db.Conn.RecreateSourceTables(); err != nil {
			return err
		}
		config.Cfg.DataSource = config.Source(target)
		if err := config.Cfg.Save(); err != nil {
			return err
		}
		logger.Success("data source switch succeeds")
		logger.Success("you have successfully change your data source to %s", target)
		return nil
	},
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Read or update uani configuration.",
}

var configPrintCmd = &cobra.Command{
	Use:   "print",
	Short: "Print the current config file.",
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := os.ReadFile(config.ConfigFilePath)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Println("config file not exist")
				return nil
			}
			return err
		}
		fmt.Print(string(data))
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <keys...> --value <value>",
	Short: "Set a config value by key path.",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		value, _ := cmd.Flags().GetString("value")
		if err := config.SetValue(args, value); err != nil {
			return err
		}
		return nil
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <keys...>",
	Short: "Get a config value by key path.",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		value, err := config.GetValue(args)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Println("config file not exist")
				return nil
			}
			return err
		}
		fmt.Printf("config %s %v\n", strings.Join(args, "."), value)
		return nil
	},
}

var searchCmd = &cobra.Command{
	Use:     "search <keyword>",
	Aliases: []string{"s", "find"},
	Short:   "Search torrents from data source by keyword.",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		keyword := args[0]
		count, _ := cmd.Flags().GetInt("count")
		regex := optString(cmd, "regex-filter")
		doDownload, _ := cmd.Flags().GetBool("download")
		downloadAs, _ := cmd.Flags().GetString("download-as")
		dupe, _ := cmd.Flags().GetBool("dupe")
		minEpisode := optInt(cmd, "min-episode")
		maxEpisode := optInt(cmd, "max-episode")
		tag, _ := cmd.Flags().GetBool("tag")
		subtitle := optString(cmd, "subtitle")

		result := controllers.Search(keyword, count, regex, dupe, minEpisode, maxEpisode, tag, subtitle)
		if result.Status != "success" {
			printResult(result)
			return nil
		}
		var episodes []model.Episode
		if result.Data != nil {
			episodes, _ = result.Data["data"].([]model.Episode)
		}
		for _, e := range episodes {
			fmt.Println(e.Title)
		}
		if doDownload {
			records := make([]db.Download, 0, len(episodes))
			for _, e := range episodes {
				name := downloadAs
				if name == "" {
					name = e.Name
				}
				records = append(records, db.Download{
					BangumiName: name,
					Episode:     e.Episode,
					Status:      db.DownloadStatusDownloading,
					Download:    e.Download,
					Title:       e.Title,
				})
			}
			if err := ensureDB(); err != nil {
				return err
			}
			download.DownloadDownloads(records)
		}
		return nil
	},
}

var addCmd = &cobra.Command{
	Use:     "add <names...>",
	Aliases: []string{"a", "sub"},
	Short:   "Subscribe bangumi.",
	Args: func(cmd *cobra.Command, args []string) error {
		bgm, _ := cmd.Flags().GetStringArray("bgm")
		if len(args) == 0 && len(bgm) == 0 {
			return fmt.Errorf("requires at least one bangumi name or --bgm")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		episode, _ := cmd.Flags().GetInt("episode")
		latest, _ := cmd.Flags().GetBool("latest")
		if latest && episode != 0 {
			return fmt.Errorf("--latest cannot be used with --episode.")
		}
		seasonOverride := optInt(cmd, "season")
		episodeOffset := optInt(cmd, "episode-offset")
		displayName := optString(cmd, "display-name")
		savePath, _ := cmd.Flags().GetString("save-path")

		var episodePtr *int
		if !latest {
			episodePtr = &episode
		}
		bgmSubjects, _ := cmd.Flags().GetStringArray("bgm")
		for _, subject := range bgmSubjects {
			result := controllers.AddFromBgmTV(subject, episodePtr, seasonOverride, episodeOffset, displayName)
			printResult(result)
			if savePath != "" && (result.Status == "success" || result.Status == "warning") {
				name, _ := result.Data["bangumi_name"].(string)
				if name != "" {
					if err := setAddSavePath(name, savePath); err != nil {
						return err
					}
				}
			}
		}
		for _, name := range args {
			result := controllers.Add(name, episodePtr, seasonOverride, episodeOffset, displayName)
			printResult(result)
			if savePath != "" && (result.Status == "success" || result.Status == "warning") {
				if err := setAddSavePath(name, savePath); err != nil {
					return err
				}
			}
		}
		return nil
	},
}

func setAddSavePath(name, savePath string) error {
	if err := ensureDB(); err != nil {
		return err
	}
	bangumi, err := db.Conn.GetBangumiByNameContains(name)
	if err != nil {
		return err
	}
	value := strings.ReplaceAll(savePath, "{bangumi_name}", bangumi.Name)
	return config.SetValue([]string{"save_path_map", bangumi.Name}, value)
}

var deleteCmd = &cobra.Command{
	Use:     "delete <names...>",
	Aliases: []string{"rm", "del", "remove"},
	Short:   "Unsubscribe bangumi by name.",
	RunE: func(cmd *cobra.Command, args []string) error {
		clearAll, _ := cmd.Flags().GetBool("clear-all")
		yes, _ := cmd.Flags().GetBool("yes")
		if clearAll {
			printResult(controllers.Delete("", true, yes))
			return nil
		}
		if len(args) == 0 {
			return nil
		}
		for _, name := range args {
			printResult(controllers.Delete(name, false, yes))
		}
		return nil
	},
}

func followedBangumi() (map[string][]db.WeeklyBangumi, error) {
	if err := ensureDB(); err != nil {
		return nil, err
	}
	statusFollowed := db.FollowedStatusFollowed
	statusUpdated := db.FollowedStatusUpdated
	followed, err := db.Conn.GetUpdatingBangumi(&statusFollowed)
	if err != nil {
		return nil, err
	}
	updated, err := db.Conn.GetUpdatingBangumi(&statusUpdated)
	if err != nil {
		return nil, err
	}
	out := map[string][]db.WeeklyBangumi{}
	for key, items := range followed {
		out[key] = append(out[key], items...)
	}
	for key, items := range updated {
		out[key] = append(out[key], items...)
	}
	return out, nil
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List subscribed bangumi.",
	RunE: func(cmd *cobra.Command, args []string) error {
		followed, err := followedBangumi()
		if err != nil {
			return err
		}
		scripts := script.GetModels()
		if len(followed) == 0 && len(scripts) == 0 {
			logger.Warning("you have not subscribed any bangumi")
			return nil
		}
		for _, item := range scripts {
			day := strings.ToLower(item.UpdateDay)
			ep := item.Episode
			status := item.Status
			followed[day] = append(followed[day], db.WeeklyBangumi{
				Name:           item.Name,
				UpdateDay:      item.UpdateDay,
				Cover:          item.Cover,
				FollowedStatus: &status,
				Episode:        &ep,
			})
		}
		var b strings.Builder
		for _, weekday := range updateTimeOrder {
			items := followed[strings.ToLower(weekday)]
			if len(items) == 0 {
				continue
			}
			b.WriteString(logger.AnsiGreen + weekday + ". " + logger.AnsiEnd)
			for j, bangumi := range items {
				name := bangumi.Name
				if bangumi.FollowedStatus != nil && (*bangumi.FollowedStatus == db.FollowedStatusUpdated || *bangumi.FollowedStatus == db.FollowedStatusFollowed) && bangumi.Episode != nil {
					name = fmt.Sprintf("%s(%d)", name, *bangumi.Episode)
				}
				if j > 0 {
					b.WriteString(strings.Repeat(" ", 5))
				}
				subs, _ := db.Conn.GetSubtitlesByIDs(bangumi.SubtitleGroup)
				names := make([]string, 0, len(subs))
				for _, s := range subs {
					names = append(names, s.Name)
				}
				b.WriteString(fmt.Sprintf("%s: %s\n", name, strings.Join(names, ", ")))
			}
		}
		fmt.Print(b.String())
		return nil
	},
}

var filterCmd = &cobra.Command{
	Use:   "filter <name>",
	Short: "Set download filters for a bangumi.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result := controllers.Filter(args[0], optString(cmd, "subtitle"), optString(cmd, "include"), optString(cmd, "exclude"), optString(cmd, "regex"))
		if result.Data == nil {
			printResult(result)
			return nil
		}
		usable, _ := result.Data["subtitle_group"].([]string)
		fmt.Printf("Usable subtitle group: %s\n\n", strings.Join(usable, ", "))
		if err := ensureDB(); err != nil {
			return err
		}
		followed, err := db.Conn.GetFollowed(args[0])
		if err != nil {
			var name string
			if n, ok := result.Data["name"].(string); ok {
				name = n
				followed, err = db.Conn.GetFollowed(name)
			}
		}
		if err != nil {
			return err
		}
		subs, _ := db.Conn.GetSubtitlesByIDs(followed.Subtitle)
		names := make([]string, 0, len(subs))
		for _, s := range subs {
			names = append(names, s.Name)
		}
		var subtitleValue string
		if len(followed.Subtitle) > 0 {
			subtitleValue = pythonStringList(names)
		} else {
			subtitleValue = "None"
		}
		includeValue := "None"
		if len(followed.Include) > 0 {
			includeValue = pythonStringList(followed.Include)
		}
		excludeValue := "None"
		if len(followed.Exclude) > 0 {
			excludeValue = pythonStringList(followed.Exclude)
		}
		regexValue := "None"
		if followed.Regex != "" {
			regexValue = followed.Regex
		}
		fmt.Printf("Followed subtitle group: %s\n", subtitleValue)
		fmt.Printf("Include keywords: %s\n", includeValue)
		fmt.Printf("Exclude keywords: %s\n", excludeValue)
		fmt.Printf("Regular expression: %s\n", regexValue)
		return nil
	},
}

var calCmd = &cobra.Command{
	Use:     "cal",
	Aliases: []string{"c", "calendar"},
	Short:   "Show the weekly bangumi calendar.",
	RunE: func(cmd *cobra.Command, args []string) error {
		forceUpdate, _ := cmd.Flags().GetBool("update")
		forceLegacy, _ := cmd.Flags().GetBool("force-update")
		today, _ := cmd.Flags().GetBool("today")
		downloadCover, _ := cmd.Flags().GetBool("cover")
		downloadCoverLegacy, _ := cmd.Flags().GetBool("download-cover")
		forceUpdate = forceUpdate || forceLegacy
		downloadCover = downloadCover || downloadCoverLegacy

		var cover []string
		if downloadCover {
			cover = script.GetDownloadCover()
		}
		weekly, err := controllers.Cal(forceUpdate, cover)
		if err != nil {
			return err
		}

		orderWithoutUnknown := updateTimeOrder[:len(updateTimeOrder)-1]
		pythonWeekday := (int(time.Now().Weekday()) + 6) % 7
		var weekdayOrder []string
		if today {
			weekdayOrder = []string{orderWithoutUnknown[pythonWeekday]}
		} else {
			n := pythonWeekday % len(orderWithoutUnknown)
			weekdayOrder = append(weekdayOrder, orderWithoutUnknown[n:]...)
			weekdayOrder = append(weekdayOrder, orderWithoutUnknown[:n]...)
		}

		col := 0
		for _, items := range weekly {
			for _, item := range items {
				if w := runewidth.StringWidth(item.Name); w > col {
					col = w
				}
			}
		}
		if col < 1 {
			col = 1
		}
		envColumns := utils.GetTerminalCol()
		if envColumns < col {
			logger.Warning("terminal window is too small.")
			envColumns = col
		}
		row := envColumns / col
		if row > 3 {
			row = 3
		}
		if row < 1 {
			row = 1
		}

		allOrder := append(append([]string{}, weekdayOrder...), "Unknown")
		for _, weekday := range allOrder {
			items := weekly[strings.ToLower(weekday)]
			if len(items) == 0 {
				continue
			}
			heading := weekday
			if today {
				heading = fmt.Sprintf("Bangumi Schedule for Today (%s)", weekday)
			}
			fmt.Printf("%s%s. %s\n", logger.AnsiGreen, heading, logger.AnsiEnd)
			num := col - 3
			if num < 0 {
				num = 0
			}
			split := strings.Repeat("-", num) + "   "
			fmt.Println(strings.Repeat(split, row))

			sort.SliceStable(items, func(i, j int) bool {
				ei, ej := -999, -999
				if items[i].Episode != nil {
					ei = *items[i].Episode
				}
				if items[j].Episode != nil {
					ej = *items[j].Episode
				}
				return ei > ej
			})

			for i := range items {
				bangumi := &items[i]
				name := bangumi.Name
				if bangumi.Status != nil && (*bangumi.Status == db.FollowedStatusUpdated || *bangumi.Status == db.FollowedStatusFollowed) && bangumi.Episode != nil {
					name = fmt.Sprintf("%s(%d)", name, *bangumi.Episode)
				}
				width := runewidth.StringWidth(name)
				spaceCount := col - 2 - width
				for _, s := range spacialAppendChars {
					if strings.Contains(name, s) {
						spaceCount += strings.Count(name, s)
					}
				}
				if bangumi.Status != nil && *bangumi.Status == db.FollowedStatusFollowed {
					name = logger.AnsiYellow + name + logger.AnsiEnd
				}
				if bangumi.Status != nil && *bangumi.Status == db.FollowedStatusUpdated {
					name = logger.AnsiGreen + name + logger.AnsiEnd
				}
				if spaceCount < 0 {
					spaceCount = 0
				}
				fmt.Printf(" %s%s", name, strings.Repeat(" ", spaceCount))
				if (i+1)%row == 0 || i+1 == len(items) {
					fmt.Println()
				}
			}
			fmt.Println()
		}
		return nil
	},
}

var fetchCmd = &cobra.Command{
	Use:     "fetch <name>",
	Aliases: []string{"f", "eps"},
	Short:   "Fetch episode list for a subscribed bangumi.",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := ensureDB(); err != nil {
			return err
		}
		bangumi, err := db.Conn.GetBangumiByName(name)
		if err != nil {
			return fmt.Errorf("Bangumi %s not exist", name)
		}
		if _, err := db.Conn.GetFollowed(name); err != nil {
			logger.Error("Bangumi %s is not followed", name)
			return nil
		}
		logger.Info("Fetch bangumi %s ...", bangumi.Name)
		notIgnore, _ := cmd.Flags().GetBool("not-ignore")
		src, err := source.New()
		if err != nil {
			return err
		}
		data, err := source.GetMaximumEpisode(src, bangumi, !notIgnore, config.Cfg.MaxPath)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			logger.Warning("Nothing.")
			return nil
		}
		maxEpisode := 0
		for _, e := range data {
			if e.Episode > maxEpisode {
				maxEpisode = e.Episode
			}
		}
		digest := len(fmt.Sprint(maxEpisode))
		for _, e := range data {
			episode := fmt.Sprintf("%*d", digest, e.Episode)
			fmt.Printf("%s | %s\n", episode, e.Title)
		}
		return nil
	},
}

var updateCmd = &cobra.Command{
	Use:     "update [names...]",
	Aliases: []string{"up"},
	Short:   "Update bangumi calendar and download new episodes.",
	RunE: func(cmd *cobra.Command, args []string) error {
		notIgnore, _ := cmd.Flags().GetBool("not-ignore")
		if err := controllers.Update(args, true, notIgnore); err != nil {
			return err
		}
		if config.Cfg.EnablePathFormatter {
			postprocessor.ProcessCompletedDownloads()
		}
		return nil
	},
}

var genCmd = &cobra.Command{
	Use:   "gen <tpl>",
	Short: "Generate config file from template.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if args[0] != "nginx.conf" {
			return fmt.Errorf("invalid value %q, choose from nginx.conf", args[0])
		}
		serverName, _ := cmd.Flags().GetString("server-name")
		osSep := string(os.PathSeparator)
		fmt.Printf(`server {
    listen 80;
    server_name %s;

    root %s%s;
    autoindex on;
    charset utf-8;

    location /bangumi/ {
        # %s
        alias %s%s;
    }

    location /api {
        proxy_pass http://127.0.0.1:8888;
    }

    location /resource {
        proxy_pass http://127.0.0.1:8888;
    }

    location / {
        # %s
        alias %s%s;
    }
}
`, serverName, config.Cfg.FrontStaticPath, osSep,
			config.Cfg.SavePath, config.Cfg.SavePath, osSep,
			config.Cfg.FrontStaticPath, config.Cfg.FrontStaticPath, osSep)
		return nil
	},
}

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "List your bangumi subscription history.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := ensureDB(); err != nil {
			return err
		}
		followed, err := db.Conn.AllFollowed()
		if err != nil {
			return err
		}
		sort.SliceStable(followed, func(i, j int) bool { return followed[i].UpdatedTime < followed[j].UpdatedTime })
		weekly, err := db.Conn.GetUpdatingBangumi(nil)
		if err != nil {
			return err
		}
		updating := map[string]bool{}
		for _, items := range weekly {
			for _, item := range items {
				updating[item.Name] = true
			}
		}
		months := []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
		year, month := 0, 0
		fmt.Println("Bangumi Timeline")
		for _, item := range followed {
			var slogan, color string
			if item.Status == db.FollowedStatusDeleted {
				slogan = "ABANDON"
				color = logger.AnsiRed
			} else if updating[item.BangumiName] {
				slogan = "FOLLOWING"
				color = logger.AnsiYellow
			} else {
				slogan = "FINISHED"
				color = logger.AnsiGreen
			}
			date := time.Unix(item.UpdatedTime, 0)
			if date.Year() != 1970 {
				if date.Year() != year {
					fmt.Printf("%s%d%s\n", logger.AnsiGreen, date.Year(), logger.AnsiEnd)
					year = date.Year()
				}
				if date.Year() == year && int(date.Month()) != month {
					fmt.Printf("  |\n  |--- %s%s%s\n  |      |\n", logger.AnsiYellow, months[int(date.Month())-1], logger.AnsiEnd)
					month = int(date.Month())
				}
				fmt.Printf("  |      |--- [%s%-9s%s] (%-2d) %s\n", color, slogan, logger.AnsiEnd, item.Episode(), item.BangumiName)
			}
		}
		return nil
	},
}

var debugCmd = &cobra.Command{
	Use:   "debug",
	Short: "Debug and diagnostic commands.",
}

var debugInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Print uani runtime and environment information.",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("uani version: `%s`\n", config.Version)
		fmt.Printf("go version: `%s`\n", runtime.Version())
		fmt.Printf("os: `%s`\n", runtime.GOOS)
		fmt.Printf("arch: `%s`\n", runtime.GOARCH)
		return nil
	},
}

var postprocessCmd = &cobra.Command{
	Use:   "postprocess",
	Short: "Process completed downloads and move to formatted paths.",
	RunE: func(cmd *cobra.Command, args []string) error {
		postprocessor.ProcessCompletedDownloads()
		return nil
	},
}

var httpCmd = &cobra.Command{
	Use:     "http",
	Aliases: []string{"serve", "web"},
	Short:   "Start the HTTP API, web frontend, and MCP server.",
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		address, _ := cmd.Flags().GetString("address")
		noWeb, _ := cmd.Flags().GetBool("no-web")
		noMCP, _ := cmd.Flags().GetBool("no-mcp")
		apiOnly, _ := cmd.Flags().GetBool("api-only")
		options := httpserver.Options{
			Web: !config.Cfg.HTTP.DisableWeb && !noWeb && !apiOnly,
			MCP: !config.Cfg.HTTP.DisableMCP && !noMCP && !apiOnly,
		}
		return httpserver.Run(address, port, options)
	},
}

func stopWebServer(cmd *cobra.Command, args []string) error {
	port, _ := cmd.Flags().GetInt("port")
	pids, err := httpserver.Stop(port)
	if err != nil {
		return err
	}
	if len(pids) == 0 {
		fmt.Printf("No process is listening on port %d.\n", port)
		return nil
	}
	parts := make([]string, 0, len(pids))
	for _, pid := range pids {
		parts = append(parts, fmt.Sprintf("%d", pid))
	}
	fmt.Printf("Stopped web server on port %d (PID: %s).\n", port, strings.Join(parts, ", "))
	return nil
}

var stopCmd = &cobra.Command{
	Use:     "stop",
	Aliases: []string{"kill-web"},
	Short:   "Stop the running uani web/HTTP server.",
	Args:    cobra.NoArgs,
	RunE:    stopWebServer,
}

var httpStopCmd = &cobra.Command{
	Use:     "stop",
	Aliases: []string{"kill"},
	Short:   "Stop the running uani web/HTTP server.",
	Args:    cobra.NoArgs,
	RunE:    stopWebServer,
}

var coverCmd = &cobra.Command{
	Use:   "cover",
	Short: "Download bangumi covers to local storage.",
	RunE: func(cmd *cobra.Command, args []string) error {
		controllers.DownloadCovers(true)
		return nil
	},
}

var kazumiCmd = &cobra.Command{
	Use:   "kazumi",
	Short: "Manage Kazumi rules and network acceleration.",
}

var kazumiCatalogCmd = &cobra.Command{
	Use:     "catalog [keyword]",
	Aliases: []string{"shop", "browse"},
	Short:   "Browse the Kazumi rule repository.",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := kazumi.CurrentRepository()
		if err != nil {
			return err
		}
		keyword := ""
		if len(args) > 0 {
			keyword = args[0]
		}
		entries, err := repo.Catalog(keyword)
		if err != nil {
			return err
		}
		writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "NAME\tVERSION\tINSTALLED\tUPDATED\tAUTHOR")
		for _, entry := range entries {
			installed := ""
			if entry.Installed {
				installed = entry.InstalledVersion
				if installed == "" {
					installed = "yes"
				}
			}
			updated := ""
			if entry.LastUpdate > 0 {
				updated = time.UnixMilli(entry.LastUpdate).Format("2006-01-02")
			}
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", entry.Name, entry.Version, installed, updated, entry.Author)
		}
		return writer.Flush()
	},
}

var kazumiListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"rules"},
	Short:   "List installed Kazumi rules.",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := kazumi.CurrentRepository()
		if err != nil {
			return err
		}
		rules, err := repo.ListInstalled()
		if err != nil {
			return err
		}
		writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "NAME\tVERSION\tAPI\tTYPE\tNATIVE\tANTI-CRAWL\tUPDATED")
		for _, rule := range rules {
			updated := ""
			if rule.UpdatedAt > 0 {
				updated = time.Unix(rule.UpdatedAt, 0).Format("2006-01-02")
			}
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%t\t%t\t%s\n",
				rule.Name, rule.Version, rule.API, rule.Type,
				rule.UseNativePlayer, rule.AntiCrawlerEnabled, updated)
		}
		return writer.Flush()
	},
}

var kazumiInstallCmd = &cobra.Command{
	Use:     "install <names...>",
	Aliases: []string{"add"},
	Short:   "Install selected rules from the Kazumi rule repository.",
	RunE: func(cmd *cobra.Command, args []string) error {
		all, _ := cmd.Flags().GetBool("all")
		repo, err := kazumi.CurrentRepository()
		if err != nil {
			return err
		}
		names := args
		if all {
			entries, err := repo.Catalog("")
			if err != nil {
				return err
			}
			names = make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, entry.Name)
			}
		}
		if len(names) == 0 {
			return fmt.Errorf("requires at least one rule name or --all")
		}
		results, err := repo.Install(names)
		if err != nil {
			return err
		}
		printKazumiInstallResults(results)
		return nil
	},
}

var kazumiUpdateCmd = &cobra.Command{
	Use:   "update [names...]",
	Short: "Update installed Kazumi rules (all when no name is given).",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := kazumi.CurrentRepository()
		if err != nil {
			return err
		}
		results, err := repo.Update(args)
		if err != nil {
			return err
		}
		printKazumiInstallResults(results)
		return nil
	},
}

var kazumiRemoveCmd = &cobra.Command{
	Use:     "remove <names...>",
	Aliases: []string{"delete"},
	Short:   "Remove installed Kazumi rules.",
	Args:    cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := kazumi.CurrentRepository()
		if err != nil {
			return err
		}
		removed, err := repo.Remove(args)
		for _, name := range removed {
			fmt.Printf("Removed %s\n", name)
		}
		return err
	},
}

var kazumiRepositoryCmd = &cobra.Command{
	Use:     "repository [url]",
	Aliases: []string{"repo", "source"},
	Short:   "Show or set the Kazumi rule repository URL.",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reset, _ := cmd.Flags().GetBool("default")
		if reset && len(args) > 0 {
			return fmt.Errorf("--default cannot be used with a URL")
		}
		settings := kazumi.CurrentSettings()
		if len(args) == 0 && !reset {
			printKazumiSettings(settings)
			return nil
		}
		value := ""
		if len(args) > 0 {
			value = args[0]
		}
		repo, err := kazumi.NormalizeRuleRepository(value)
		if err != nil {
			return err
		}
		settings.RuleRepository = repo
		if err := kazumi.SaveSettings(settings); err != nil {
			return err
		}
		fmt.Printf("rule_repository = %s\n", repo)
		return nil
	},
}

var kazumiSearchCmd = &cobra.Command{
	Use:     "search <rule> <keyword>",
	Aliases: []string{"find"},
	Short:   "Search bangumi through an installed Kazumi rule.",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		rule, err := kazumi.CurrentRule(args[0])
		if err != nil {
			return err
		}
		results, err := kazumi.Search(rule, args[1])
		if err != nil {
			return err
		}
		writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "NAME\tSOURCE")
		for _, item := range results {
			fmt.Fprintf(writer, "%s\t%s\n", item.Name, item.Src)
		}
		return writer.Flush()
	},
}

var kazumiChaptersCmd = &cobra.Command{
	Use:     "chapters <rule> <source>",
	Aliases: []string{"episodes", "eps"},
	Short:   "List episode roads for a Kazumi search result.",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		rule, err := kazumi.CurrentRule(args[0])
		if err != nil {
			return err
		}
		roads, err := kazumi.Chapters(rule, args[1])
		if err != nil {
			return err
		}
		for _, road := range roads {
			fmt.Printf("%s\n", road.Name)
			for index, url := range road.URLs {
				name := ""
				if index < len(road.Names) {
					name = road.Names[index]
				}
				fmt.Printf("  %-16s %s\n", name, url)
			}
		}
		return nil
	},
}

var kazumiDownloadCmd = &cobra.Command{
	Use:     "download [rule] [name] [episode] [source]",
	Aliases: []string{"dl", "get"},
	Short:   "Download an episode through a Kazumi rule (yt-dlp / ffmpeg).",
	Args:    cobra.MaximumNArgs(4),
	RunE: func(cmd *cobra.Command, args []string) error {
		pick := func(index int, flagValue string) string {
			if strings.TrimSpace(flagValue) != "" {
				return strings.TrimSpace(flagValue)
			}
			if index < len(args) {
				return strings.TrimSpace(args[index])
			}
			return ""
		}
		ruleFlag, _ := cmd.Flags().GetString("rule")
		nameFlag, _ := cmd.Flags().GetString("name")
		episodeFlag, _ := cmd.Flags().GetString("episode")
		sourceFlag, _ := cmd.Flags().GetString("source")
		rule := pick(0, ruleFlag)
		name := pick(1, nameFlag)
		episodeText := pick(2, episodeFlag)
		source := pick(3, sourceFlag)
		if rule == "" || name == "" || episodeText == "" || source == "" {
			return fmt.Errorf("usage: uani kazumi download <rule> <name> <episode> <source>\n   or: uani kazumi download --rule <rule> --name <name> --episode <n> --source <url>")
		}
		var episode int
		if _, err := fmt.Sscanf(episodeText, "%d", &episode); err != nil {
			return err
		}
		title, _ := cmd.Flags().GetString("title")
		task, err := controllers.StartKazumiDownload(controllers.KazumiDownloadOptions{
			Rule:    rule,
			Name:    name,
			Episode: episode,
			Source:  source,
			Title:   title,
		})
		if err != nil {
			return err
		}
		logger.Success("Kazumi download queued (task %d): %s EP%d", task.ID, task.Name, task.Episode)
		noWait, _ := cmd.Flags().GetBool("no-wait")
		if noWait {
			fmt.Println("Check progress with `uani kazumi downloads`.")
			return nil
		}
		for {
			current, err := db.Conn.GetKazumiDownload(task.ID)
			if err != nil {
				return err
			}
			switch current.Status {
			case db.KazumiDownloadDone:
				logger.Success("Kazumi download finished: %s", current.Path)
				return nil
			case db.KazumiDownloadFailed:
				return fmt.Errorf("Kazumi download failed: %s", current.Error)
			case db.KazumiDownloadCanceled:
				return fmt.Errorf("Kazumi download canceled")
			}
			time.Sleep(2 * time.Second)
		}
	},
}

var kazumiDownloadsCmd = &cobra.Command{
	Use:     "downloads",
	Aliases: []string{"tasks", "ls", "status"},
	Short:   "List Kazumi streaming download tasks.",
	RunE: func(cmd *cobra.Command, args []string) error {
		tasks, err := controllers.ListKazumiDownloads()
		if err != nil {
			return err
		}
		writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "ID\tNAME\tEP\tSTATUS\tSIZE\tPATH")
		for _, task := range tasks {
			status := map[int]string{
				db.KazumiDownloadPending:  "pending",
				db.KazumiDownloadRunning:  "running",
				db.KazumiDownloadDone:     "done",
				db.KazumiDownloadFailed:   "failed",
				db.KazumiDownloadCanceled: "canceled",
			}[task.Status]
			fmt.Fprintf(writer, "%d\t%s\t%d\t%s\t%d\t%s\n", task.ID, task.Name, task.Episode, status, task.Size, task.Path)
		}
		return writer.Flush()
	},
}

var kazumiAccelerationCmd = &cobra.Command{Use: "acceleration [bangumi|rules|image] [mode]",
	Aliases: []string{"network", "proxy"},
	Short:   "Show or set Kazumi network acceleration modes.",
	Args:    cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		settings := kazumi.CurrentSettings()
		if len(args) == 0 {
			printKazumiSettings(settings)
			return nil
		}
		target := normalizeKazumiTarget(args[0])
		if target == "" {
			return fmt.Errorf("unknown acceleration target %q (use bangumi, rules, or image)", args[0])
		}
		if len(args) == 1 {
			printKazumiAcceleration(target, settings)
			return nil
		}
		switch target {
		case "bangumi":
			value, err := kazumi.NormalizeBangumiAcceleration(args[1])
			if err != nil {
				return err
			}
			settings.BangumiAcceleration = value
		case "rules":
			value, err := kazumi.NormalizeRulesMirror(args[1])
			if err != nil {
				return err
			}
			settings.RulesMirror = value
		case "image":
			value, err := kazumi.NormalizeImageAcceleration(args[1])
			if err != nil {
				return err
			}
			settings.ImageAcceleration = value
		}
		if err := kazumi.SaveSettings(settings); err != nil {
			return err
		}
		printKazumiAcceleration(target, settings)
		return nil
	},
}

var kazumiMirrorCmd = &cobra.Command{
	Use:     "mirror [url]",
	Aliases: []string{"mirrors"},
	Short:   "List, add, switch, remove or test Bangumi API mirrors.",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		settings := kazumi.CurrentSettings()
		add, _ := cmd.Flags().GetString("add")
		remove, _ := cmd.Flags().GetString("remove")
		test, _ := cmd.Flags().GetBool("test")
		if add != "" && remove != "" {
			return fmt.Errorf("--add and --remove cannot be used together")
		}
		if add != "" {
			mirror, err := kazumi.NormalizeBangumiMirror(add)
			if err != nil {
				return err
			}
			if mirror == "" {
				return fmt.Errorf("mirror URL is empty")
			}
			if !containsString(settings.BangumiMirrors, mirror) {
				settings.BangumiMirrors = append(settings.BangumiMirrors, mirror)
			}
			if settings.BangumiMirror == "" {
				settings.BangumiMirror = mirror
			}
			if err := kazumi.SaveSettings(settings); err != nil {
				return err
			}
			printKazumiMirrors(kazumi.CurrentSettings())
			return nil
		}
		if remove != "" {
			mirror, err := kazumi.NormalizeBangumiMirror(remove)
			if err != nil {
				return err
			}
			out := make([]string, 0, len(settings.BangumiMirrors))
			for _, item := range settings.BangumiMirrors {
				if item != mirror {
					out = append(out, item)
				}
			}
			settings.BangumiMirrors = out
			if settings.BangumiMirror == mirror {
				settings.BangumiMirror = ""
				if len(out) > 0 {
					settings.BangumiMirror = out[0]
				}
			}
			if err := kazumi.SaveSettings(settings); err != nil {
				return err
			}
			printKazumiMirrors(kazumi.CurrentSettings())
			return nil
		}
		if test {
			target := ""
			if len(args) > 0 {
				target = args[0]
			} else {
				target = settings.BangumiMirror
			}
			status, latency, err := kazumi.TestBangumiMirror(target)
			if err != nil {
				return err
			}
			fmt.Printf("mirror = %s\nstatus = %d\nlatency = %s\n", target, status, latency.Round(time.Millisecond))
			return nil
		}
		if len(args) == 1 {
			mirror, err := kazumi.NormalizeBangumiMirror(args[0])
			if err != nil {
				return err
			}
			if mirror == "" {
				return fmt.Errorf("mirror URL is empty")
			}
			if !containsString(settings.BangumiMirrors, mirror) {
				settings.BangumiMirrors = append(settings.BangumiMirrors, mirror)
			}
			settings.BangumiMirror = mirror
			if err := kazumi.SaveSettings(settings); err != nil {
				return err
			}
			printKazumiMirrors(kazumi.CurrentSettings())
			return nil
		}
		printKazumiMirrors(settings)
		return nil
	},
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func printKazumiMirrors(settings kazumi.Settings) {
	fmt.Printf("active = %s\n", settings.BangumiMirror)
	for _, mirror := range settings.BangumiMirrors {
		mark := " "
		if mirror == settings.BangumiMirror {
			mark = "*"
		}
		fmt.Printf("%s %s\n", mark, mirror)
	}
}

func normalizeKazumiTarget(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "bangumi", "bgm", "entry", "条目", "番剧":
		return "bangumi"
	case "rules", "rule", "repo", "repository", "仓库", "规则":
		return "rules"
	case "image", "img", "cover", "图片":
		return "image"
	default:
		return ""
	}
}

func printKazumiInstallResults(results []kazumi.InstallResult) {
	for _, result := range results {
		switch result.Status {
		case "unchanged":
			fmt.Printf("Already installed: %s (%s)\n", result.Name, result.Version)
		case "updated":
			fmt.Printf("Updated %s: %s -> %s\n", result.Name, result.PreviousVersion, result.Version)
		default:
			fmt.Printf("Installed %s (%s)\n", result.Name, result.Version)
		}
	}
}

func printKazumiSettings(settings kazumi.Settings) {
	fmt.Printf("rule_repository = %s\n", settings.RuleRepository)
	fmt.Printf("bangumi_acceleration = %s\n", settings.BangumiAcceleration)
	fmt.Printf("rules_mirror = %s\n", settings.RulesMirror)
	fmt.Printf("image_acceleration = %s\n", settings.ImageAcceleration)
	fmt.Printf("bangumi_mirror = %s\n", settings.BangumiMirror)
	fmt.Printf("bangumi_mirrors = %s\n", strings.Join(settings.BangumiMirrors, ", "))
}

func printKazumiAcceleration(target string, settings kazumi.Settings) {
	switch target {
	case "bangumi":
		fmt.Printf("bangumi_acceleration = %s\n", settings.BangumiAcceleration)
		printKazumiModes(kazumi.BangumiModes())
		printKazumiMirrors(settings)
	case "rules":
		fmt.Printf("rules_mirror = %s\n", settings.RulesMirror)
		printKazumiModes(kazumi.RulesModes())
	case "image":
		fmt.Printf("image_acceleration = %s\n", settings.ImageAcceleration)
		printKazumiModes(kazumi.ImageModes())
	}
}

func printKazumiModes(modes []kazumi.ModeOption) {
	for _, mode := range modes {
		fmt.Printf("  %-8s %s - %s\n", mode.Value, mode.Label, mode.Description)
	}
}

var syncCmd = &cobra.Command{
	Use:     "sync",
	Aliases: []string{"pull", "import", "追番"},
	Short:   "Sync subscriptions with the Bangumi (bgm.tv) 追番 list.",
	Args:    cobra.NoArgs,
	RunE:    runBangumiSync,
}

var syncLoginCmd = &cobra.Command{
	Use:     "login <access_token>",
	Aliases: []string{"token"},
	Short:   "Validate and save a Bangumi Access Token.",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		user, err := controllers.SetBangumiToken(args[0])
		if err != nil {
			return err
		}
		logger.Success("connected to Bangumi as %s", user.Username)
		fmt.Println("Run `uani sync` to import your 追番 list, or enable automatic sync with:")
		fmt.Println("  uani config set bangumi sync_enabled --value true")
		return nil
	},
}

var syncLogoutCmd = &cobra.Command{
	Use:     "logout",
	Aliases: []string{"remove-token"},
	Short:   "Remove the saved Bangumi Access Token.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := controllers.ClearBangumiToken(); err != nil {
			return err
		}
		logger.Success("Bangumi account removed")
		return nil
	},
}

var syncStatusCmd = &cobra.Command{
	Use:     "status",
	Aliases: []string{"info"},
	Short:   "Show the Bangumi subscription sync status.",
	RunE: func(cmd *cobra.Command, args []string) error {
		printBangumiSyncStatus(controllers.BangumiSyncStatus())
		return nil
	},
}

var syncTypesCmd = &cobra.Command{
	Use:   "types [types...]",
	Short: "Show or set the Bangumi collection buckets to sync.",
	RunE: func(cmd *cobra.Command, args []string) error {
		status := controllers.BangumiSyncStatus()
		if len(args) == 0 {
			fmt.Printf("sync_types = %s\n", strings.Join(status.SyncTypes, ","))
			fmt.Println("available: wish (想看), watched (看过), watching (在看), on_hold (搁置), dropped (抛弃), all")
			return nil
		}
		values := args
		if err := controllers.ConfigureBangumiSync(nil, &values); err != nil {
			return err
		}
		status = controllers.BangumiSyncStatus()
		fmt.Printf("sync_types = %s\n", strings.Join(status.SyncTypes, ","))
		return nil
	},
}

func runBangumiSync(cmd *cobra.Command, args []string) error {
	typesValue, _ := cmd.Flags().GetString("types")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	latest, _ := cmd.Flags().GetBool("latest")
	limit, _ := cmd.Flags().GetInt("limit")
	episode := optInt(cmd, "episode")
	if latest && episode != nil {
		return fmt.Errorf("--latest cannot be used with --episode")
	}
	var types []string
	if strings.TrimSpace(typesValue) != "" {
		types = strings.Split(typesValue, ",")
	}
	summary, err := controllers.SyncSubscriptions(controllers.SyncOptions{
		Types:   types,
		DryRun:  dryRun,
		Latest:  latest,
		Episode: episode,
		Limit:   limit,
	})
	if err != nil {
		return err
	}
	printBangumiSyncSummary(summary)
	return nil
}

func printBangumiSyncStatus(status controllers.BangumiSyncSettings) {
	token := "not configured"
	if status.TokenConfigured {
		token = "configured"
	}
	username := status.Username
	if username == "" {
		username = "-"
	}
	lastSync := "never"
	if status.LastSync > 0 {
		lastSync = time.Unix(status.LastSync, 0).Format("2006-01-02 15:04:05")
	}
	fmt.Printf("username = %s\n", username)
	fmt.Printf("access_token = %s\n", token)
	fmt.Printf("sync_enabled = %t\n", status.SyncEnabled)
	fmt.Printf("sync_types = %s\n", strings.Join(status.SyncTypes, ","))
	fmt.Printf("last_sync = %s\n", lastSync)
}

func printBangumiSyncSummary(summary controllers.SyncSummary) {
	if summary.Username != "" {
		logger.Info("Bangumi user: %s", summary.Username)
	}
	if len(summary.Types) > 0 {
		logger.Info("collection types: %s", strings.Join(summary.Types, ","))
	}
	for _, item := range summary.Items {
		switch item.Action {
		case "added":
			logger.Success("+ %s (episode %d)", item.Name, item.Episode)
		case "would_add":
			fmt.Printf("[dry-run] would add %s (episode %d)\n", item.Name, item.Episode)
		case "failed":
			logger.Error("! %s: %s", item.Name, item.Message)
		}
	}
	if summary.DryRun {
		logger.Info("dry run: %d collection(s) fetched, %d would be added", summary.Fetched, countSyncAction(summary, "would_add"))
		return
	}
	logger.Info("sync finished: %d fetched, %d added, %d already followed, %d failed",
		summary.Fetched, summary.Added, summary.Existing, summary.Failed)
}

func countSyncAction(summary controllers.SyncSummary, action string) int {
	count := 0
	for _, item := range summary.Items {
		if item.Action == action {
			count++
		}
	}
	return count
}

var completionCmd = &cobra.Command{
	Use:   "completion <shell>",
	Short: "Generate shell completion script.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletion(os.Stdout)
		case "zsh":
			return rootCmd.GenZshCompletion(os.Stdout)
		case "fish":
			return rootCmd.GenFishCompletion(os.Stdout, true)
		case "powershell":
			return rootCmd.GenPowerShellCompletion(os.Stdout)
		default:
			return fmt.Errorf("unsupported shell %q", args[0])
		}
	},
}

var seenCmd = &cobra.Command{
	Use:   "seen",
	Short: "Manage downloaded episode records.",
}

var seenForgetCmd = &cobra.Command{
	Use:   "forget <name> <episode>",
	Short: "Remove an episode from download records (triggers re-download on next update).",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var episode int
		if _, err := fmt.Sscanf(args[1], "%d", &episode); err != nil {
			return err
		}
		printResult(controllers.SeenForget(args[0], episode))
		return nil
	},
}

var seenMarkCmd = &cobra.Command{
	Use:   "mark <name> <episode>",
	Short: "Add an episode to download records (marks it as seen).",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var episode int
		if _, err := fmt.Sscanf(args[1], "%d", &episode); err != nil {
			return err
		}
		printResult(controllers.SeenMark(args[0], episode))
		return nil
	},
}

func init() {
	rootCmd.SetVersionTemplate(fmt.Sprintf("uani %sver. {{.Version}}%s\n\nCompatible with BGmi by RicterZ\nGithub: https://github.com/HanamiSakiP/u-ani\n", logger.AnsiYellow, logger.AnsiEnd))

	installCmd.Flags().Bool("no-web", false, "Do not download web static files.")
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(upgradeCmd)
	rootCmd.AddCommand(sourceCmd)
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configPrintCmd)
	configCmd.AddCommand(configSetCmd)
	configSetCmd.Flags().String("value", "", "")
	_ = configSetCmd.MarkFlagRequired("value")
	configCmd.AddCommand(configGetCmd)

	searchCmd.Flags().IntP("count", "c", 0, "Max page count of search results.")
	searchCmd.Flags().StringP("regex-filter", "r", "", "Regular expression filter for title.")
	searchCmd.Flags().BoolP("download", "d", false, "Download search results.")
	searchCmd.Flags().String("download-as", "", "Bangumi name for download records and directory (with --download).")
	searchCmd.Flags().Bool("dupe", false, "Show duplicated episodes.")
	searchCmd.Flags().Int("min-episode", 0, "Minimum episode number filter.")
	searchCmd.Flags().Int("max-episode", 0, "Maximum episode number filter.")
	searchCmd.Flags().Bool("tag", false, "Use tag to search (if data source supports).")
	searchCmd.Flags().String("subtitle", "", "Subtitle group filter (requires --tag).")
	rootCmd.AddCommand(searchCmd)

	addCmd.Flags().Int("episode", 0, "Mark episodes 1..N as already downloaded; use 0 to start downloading from episode 1.")
	addCmd.Flags().Bool("latest", false, "Mark all currently available episodes as already downloaded.")
	addCmd.Flags().Int("season", 0, "Set season number (overrides auto-detection, works for existing subscriptions).")
	addCmd.Flags().String("save-path", "", "Set save_path_map entry, e.g. './{bangumi_name}/S1/'.")
	addCmd.Flags().Int("episode-offset", 0, "Adjust only the path formatter episode number.")
	addCmd.Flags().String("display-name", "", "Override display name in path formatter (e.g. for TMDB matching).")
	addCmd.Flags().StringArray("bgm", nil, "Subscribe from a bgm.tv subject id or URL (repeatable).")
	rootCmd.AddCommand(addCmd)

	deleteCmd.Flags().Bool("clear-all", false, "Clear all subscriptions (names will be ignored)")
	deleteCmd.Flags().Bool("yes", false, "No confirmation")
	rootCmd.AddCommand(deleteCmd)

	rootCmd.AddCommand(listCmd)

	filterCmd.Flags().String("subtitle", "", "Subtitle group names, comma-separated.")
	filterCmd.Flags().String("include", "", "Include keywords in title, comma-separated.")
	filterCmd.Flags().String("exclude", "", "Exclude keywords from title, comma-separated.")
	filterCmd.Flags().String("regex", "", "Filter by regular expression.")
	rootCmd.AddCommand(filterCmd)

	calCmd.Flags().BoolP("update", "f", false, "Fetch the latest bangumi calendar.")
	calCmd.Flags().Bool("force-update", false, "Use --update instead.")
	calCmd.Flags().Bool("today", false, "show bangumi calendar for today.")
	calCmd.Flags().Bool("cover", false, "Download covers to local storage.")
	calCmd.Flags().Bool("download-cover", false, "Use --cover instead.")
	_ = calCmd.Flags().MarkHidden("force-update")
	_ = calCmd.Flags().MarkHidden("download-cover")
	rootCmd.AddCommand(calCmd)

	fetchCmd.Flags().BoolP("not-ignore", "n", false, "Include old rows (older than 3 months).")
	rootCmd.AddCommand(fetchCmd)

	updateCmd.Flags().BoolP("not-ignore", "n", false, "Do not ignore the old bangumi detail rows (3 month ago).")
	rootCmd.AddCommand(updateCmd)

	genCmd.Flags().String("server-name", "", "")
	rootCmd.AddCommand(genCmd)

	rootCmd.AddCommand(historyCmd)

	debugCmd.AddCommand(debugInfoCmd)
	rootCmd.AddCommand(debugCmd)

	rootCmd.AddCommand(postprocessCmd)

	httpCmd.PersistentFlags().IntP("port", "p", 8888, "Port to listen on.")
	httpCmd.PersistentFlags().StringP("address", "a", "0.0.0.0", "Address to bind.")
	httpCmd.Flags().Bool("no-web", false, "Disable the embedded Web UI.")
	httpCmd.Flags().Bool("no-mcp", false, "Disable MCP endpoints.")
	httpCmd.Flags().Bool("api-only", false, "Expose only the JSON API and media endpoints.")
	httpCmd.AddCommand(httpStopCmd)
	rootCmd.AddCommand(httpCmd)

	stopCmd.Flags().IntP("port", "p", 8888, "Port of the running web server.")
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(coverCmd)

	kazumiInstallCmd.Flags().Bool("all", false, "Install every rule in the repository.")
	kazumiMirrorCmd.Flags().String("add", "", "Add a Bangumi mirror to the list.")
	kazumiMirrorCmd.Flags().String("remove", "", "Remove a Bangumi mirror from the list.")
	kazumiMirrorCmd.Flags().Bool("test", false, "Test the active or specified mirror.")
	kazumiRepositoryCmd.Flags().Bool("default", false, "Reset to the official KazumiRules repository.")
	kazumiCmd.AddCommand(kazumiCatalogCmd)
	kazumiCmd.AddCommand(kazumiListCmd)
	kazumiCmd.AddCommand(kazumiInstallCmd)
	kazumiCmd.AddCommand(kazumiUpdateCmd)
	kazumiCmd.AddCommand(kazumiRemoveCmd)
	kazumiCmd.AddCommand(kazumiRepositoryCmd)
	kazumiCmd.AddCommand(kazumiAccelerationCmd)
	kazumiCmd.AddCommand(kazumiMirrorCmd)
	kazumiDownloadCmd.Flags().String("title", "", "Episode title used in download records.")
	kazumiDownloadCmd.Flags().String("rule", "", "Kazumi rule name (alternative to the first positional argument).")
	kazumiDownloadCmd.Flags().String("name", "", "Bangumi display name (alternative to the second positional argument).")
	kazumiDownloadCmd.Flags().StringP("episode", "e", "", "Episode number (alternative to the third positional argument).")
	kazumiDownloadCmd.Flags().String("source", "", "Episode source URL (alternative to the fourth positional argument).")
	kazumiDownloadCmd.Flags().Bool("no-wait", false, "Return immediately instead of waiting for the download.")
	kazumiCmd.AddCommand(kazumiSearchCmd)
	kazumiCmd.AddCommand(kazumiChaptersCmd)
	kazumiCmd.AddCommand(kazumiDownloadCmd)
	kazumiCmd.AddCommand(kazumiDownloadsCmd)
	rootCmd.AddCommand(kazumiCmd)

	syncCmd.Flags().String("types", "", "Collection types to import: wish,watched,watching,on_hold,dropped or all.")
	syncCmd.Flags().Bool("dry-run", false, "Show what would be subscribed without changing anything.")
	syncCmd.Flags().Bool("latest", false, "Mark all currently available episodes as seen.")
	syncCmd.Flags().Int("episode", 0, "Mark episodes 1..N as seen instead of using Bangumi ep_status.")
	syncCmd.Flags().Int("limit", 0, "Maximum number of collections to process.")
	syncCmd.AddCommand(syncLoginCmd)
	syncCmd.AddCommand(syncLogoutCmd)
	syncCmd.AddCommand(syncStatusCmd)
	syncCmd.AddCommand(syncTypesCmd)
	rootCmd.AddCommand(syncCmd)

	rootCmd.AddCommand(completionCmd)

	seenCmd.AddCommand(seenForgetCmd)
	seenCmd.AddCommand(seenMarkCmd)
	rootCmd.AddCommand(seenCmd)
}

func Main() {
	if err := config.Load(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logger.Init(config.Cfg.LogPath)
	defer logger.Close()
	if err := rootCmd.Execute(); err != nil {
		logger.Error("%v", err)
		os.Exit(1)
	}
}
