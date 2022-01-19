package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/penguinpowernz/stonkcritter/api"
	"github.com/penguinpowernz/stonkcritter/bot"
	SINKS "github.com/penguinpowernz/stonkcritter/sinks"
	"github.com/penguinpowernz/stonkcritter/source"
	"github.com/penguinpowernz/stonkcritter/watcher"
)

var (
	dataDir    = "./data"
	fileSource = ""
	cursorFile = "./stonkcritter.cursor"

	runAPI, runChat, quiet, downloadDump bool
)

func main() {
	flag.StringVar(&dataDir, "d", dataDir, "the directory to save bot brain data in")
	flag.StringVar(&fileSource, "f", fileSource, "read from the given source file instead of S3")
	flag.BoolVar(&runChat, "chat", false, "enable Telegram communication")
	flag.BoolVar(&runAPI, "api", false, "enable informational API")
	flag.BoolVar(&quiet, "q", false, "don't log disclosure messages to terminal")
	flag.BoolVar(&downloadDump, "download", false, "download the disclosures and dump them to STDOUT")
	flag.Parse()

	shouldBroadcast := os.Getenv("BOT_TOKEN") != "" && os.Getenv("BOT_CHANNEL") != ""
	shouldChat := os.Getenv("BOT_TOKEN") != "" && runChat

	if downloadDump {
		downloadAndDump()
		os.Exit(0)
	}

	var opts []watcher.Option
	if fileSource != "" {
		opts = append(opts, watcher.FromFile(fileSource))
		log.Println("using file disclosure source", fileSource)
	} else {
		opts = append(opts, watcher.FromS3())
		log.Println("using S3 disclosure source")
	}

	if cursorFile != "" {
		setCursorToTodayIfNotExist(cursorFile)
		opts = append(opts, watcher.DiskCursor(cursorFile, true))
		log.Println("using cursor file", cursorFile)
	}

	w, err := watcher.NewWatcher(opts...)
	if err != nil {
		panic(err)
	}

	var sinks []SINKS.Sink

	if shouldBroadcast {
		broadcast, err := SINKS.TelegramChannel(os.Getenv("BOT_TOKEN"), os.Getenv("BOT_CHANNEL"))
		if err != nil {
			fmt.Println("WARN: failed to parse BOT_CHANNEL:", err)
		} else {
			sinks = append(sinks, broadcast)
			log.Println("added sink: telegram broadcast to channel", os.Getenv("BOT_CHANNEL"))
		}
	}

	if !quiet {
		sinks = append(sinks, SINKS.Writer(os.Stdout))
		log.Println("added sink: stdout")
	}

	if shouldChat {
		brain, err := bot.NewBrain(dataDir)
		if err != nil {
			panic(err)
		}

		bot, err := bot.NewBot(brain, os.Getenv("BOT_TOKEN"))
		if err != nil {
			panic(err)
		}

		sinks = append(sinks, SINKS.TelegramBot(bot))
		log.Println("added sink: telegram bot")

		if runAPI {
			log.Println("starting informational API")
			go startAPI(w, brain, bot)
		}
	}

	log.Println("started the disclosure watcher")
	w.Start(context.Background())
	for w.Next() {
		for _, sink := range sinks {
			sink(w.Disclosure())
		}
	}
}

func startAPI(w *watcher.Watcher, b *bot.Brain, bt *bot.Bot) {
	server := api.NewServer(struct {
		*watcher.Watcher
		*bot.Brain
		*bot.Bot
	}{w, b, bt})
	r := gin.Default()
	server.SetupRoutes(r)
	r.Run(":8090")
}

func setCursorToTodayIfNotExist(fn string) {
	_, err := os.Stat(fn)
	if os.IsNotExist(err) {
		ioutil.WriteFile(fn, []byte(strconv.Itoa(int(time.Now().Unix()))), 0644)
	}
}

func downloadAndDump() {
	dd, err := source.GetDisclosuresFromS3()
	if err != nil {
		panic(err)
	}
	data, err := json.Marshal(dd)
	if err != nil {
		panic(err)
	}
	os.Stdout.Write(data)
}
