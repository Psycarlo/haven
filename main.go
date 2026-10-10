package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/fiatjaf/khatru"
	"github.com/nbd-wtf/go-nostr"
	"github.com/spf13/afero"

	"github.com/barrydeen/haven/pkg/wot"
)

var (
	pool   *nostr.SimplePool
	config = loadConfig()
	fs     afero.Fs
)

func main() {
	nostr.InfoLogger = log.New(io.Discard, "", 0)
	slog.SetLogLoggerLevel(getLogLevelFromConfig())
	green := "\033[32m"
	reset := "\033[0m"
	fmt.Println(green + art + reset)

	mainCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fs = afero.NewOsFs()
	if err := fs.MkdirAll(config.BlossomPath, 0755); err != nil {
		log.Fatal("🚫 error creating blossom path:", err)
	}

	pool = nostr.NewSimplePool(mainCtx,
		nostr.WithPenaltyBox(),
		nostr.WithRelayOptions(
			nostr.WithRequestHeader{
				"User-Agent": []string{config.UserAgent},
			}),
	)

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "backup":
			runBackup(mainCtx)
			return
		case "restore":
			runRestore(mainCtx)
			return
		case "import":
			ensureImportRelays()
			runImport(mainCtx)
			return
		case "help":
			printHelp()
			return
		}

		if os.Args[1] == "-h" || os.Args[1] == "--help" {
			printHelp()
			return
		}
	}

	flag.Parse()

	log.Println("🚀 HAVEN", config.RelayVersion, "is booting up")
	defer log.Println("🔌 HAVEN is shutting down")
	log.Println("👥 Number of whitelisted pubkeys:", config.WhitelistedPubKeys.Len())
	log.Println("🚷 Number of blacklisted pubkeys:", config.BlacklistedPubKeys.Len())

	log.Println("🛰️ Relays:", strings.Join(enabledRelayNames(), ", "))

	// Only the chat and inbox relays reach out to public relays: for the web of
	// trust, and to pull in events that tag whitelisted pubkeys.
	usesPublicRelays := config.Relays["chat"] || config.Relays["inbox"]
	if usesPublicRelays {
		ensureImportRelays()
		wotModel := wot.NewSimpleInMemory(
			pool,
			config.WhitelistedPubKeys.Set,
			config.ImportSeedRelays,
			config.WotDepth,
			config.WotMinimumFollowers,
			config.WotFetchTimeoutSeconds,
		)
		wot.Initialize(mainCtx, wotModel)
	}
	initRelays(mainCtx)

	go func() {
		if usesPublicRelays {
			go subscribeInboxAndChat(mainCtx)
			go wot.PeriodicRefresh(mainCtx, config.WotRefreshInterval)
		}
		go startPeriodicCloudBackups(mainCtx)
		go watchPubkeyLists(mainCtx, config.ListsReloadInterval)
	}()

	http.Handle("GET /health", healthHandler(privateDB))
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("templates/static"))))
	http.HandleFunc("/", dynamicRelayHandler)

	addr := fmt.Sprintf("%s:%d", config.RelayBindAddress, config.RelayPort)

	log.Printf("🔗 listening at %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal("🚫 error starting server:", err)
	}
}

func printHelp() {
	fmt.Println("haven is a personal nostr relay.")
	fmt.Println()
	fmt.Println("usage: haven [command]")
	fmt.Println()
	fmt.Println("commands:")
	fmt.Println("  backup  - backup the database")
	fmt.Println("  restore - restore the database")
	fmt.Println("  import  - import notes from seed relays")
	fmt.Println("  help    - show this help message")
	fmt.Println()
	fmt.Println("if no command is provided, the relay starts by default.")
	fmt.Println()
	fmt.Println("run 'haven [command] --help' for more information on a command.")
}

// relayForPath names the relay a request is for. Everything that isn't another
// relay's path belongs to the outbox relay, including its Blossom routes.
func relayForPath(path string) (string, *khatru.Relay) {
	switch path {
	case "/private":
		return "private", privateRelay
	case "/chat":
		return "chat", chatRelay
	case "/inbox":
		return "inbox", inboxRelay
	default:
		return "outbox", outboxRelay
	}
}

func dynamicRelayHandler(w http.ResponseWriter, r *http.Request) {
	name, relay := relayForPath(r.URL.Path)
	if !config.Relays[name] {
		http.NotFound(w, r)
		return
	}
	relay.ServeHTTP(w, r)
}

func enabledRelayNames() []string {
	var names []string
	for _, name := range relayNames {
		if config.Relays[name] {
			names = append(names, name)
		}
	}
	return names
}

func getLogLevelFromConfig() slog.Level {
	switch config.LogLevel {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo // Default level
	}
}
