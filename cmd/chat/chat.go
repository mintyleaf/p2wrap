package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"p2p4ai/internal/chat"
	"p2p4ai/pkg/p2p"

	"github.com/sirupsen/logrus"
)

const figlet = `

W E L C O M E  T O
					     db                  db   
					     88                  88   
.8d888b. .d8888b. .d8888b. .d8888b. .d8888b. 88d888b. .d8888b. d8888P 
88'  '88 88ooood8 88ooood8 88'  '88 88'      88'  '88 88'  '88   88   
88.  .88 88.      88.      88       88.      88    88 88.  .88   88   
888888P' '88888P' '88888P' db       '88888P' db    db '8888888   '88P   
88                                                                    
dP
`

func init() {
	// Log as Text with color
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors:     true,
		FullTimestamp:   true,
		TimestampFormat: time.RFC822,
		DisableQuote:    true,
	})

	// Log to stdout
	logrus.SetOutput(os.Stdout)
}

func main() {
	// Define input flags
	username := flag.String("user", "", "username to use in the chatroom.")
	chatroom := flag.String("room", "", "chatroom to join.")
	loglevel := flag.String("log", "", "level of logs to print.")
	discovery := flag.String("discover", "advertise", "method to use for discovery.")
	useDHT := flag.Bool("dht", true, "use dht")
	useMDNS := flag.Bool("mdns", true, "use mdns")
	useDefaultBootstrapPeers := flag.Bool(
		"default_bootstrap_peers", true,
		"use default bootstrap peers (if custom peers is set - add default to the bottom of the list)")
	customBootstrapPeerAddrsStr := flag.String(
		"bootstrap_peers",
		"", "comma separated list of bootstrap peers")
	customListenAddrsStr := flag.String(
		"listen_addrs",
		"", "comma separated list of listen maddrs. fallback to libp2p defaults if empty")
	customRelayAddrsStr := flag.String(
		"relay_addrs",
		"", "comma separated list of static relay maddrs",
	)
	rendezvous := flag.String("rendezvous", p2p.Service, "rendezvous string. used if discovery is set to advertise")
	// Parse input flags
	flag.Parse()

	// Set the log level
	switch *loglevel {
	case "panic", "PANIC":
		logrus.SetLevel(logrus.PanicLevel)
	case "fatal", "FATAL":
		logrus.SetLevel(logrus.FatalLevel)
	case "error", "ERROR":
		logrus.SetLevel(logrus.ErrorLevel)
	case "warn", "WARN":
		logrus.SetLevel(logrus.WarnLevel)
	case "info", "INFO":
		logrus.SetLevel(logrus.InfoLevel)
	case "debug", "DEBUG":
		logrus.SetLevel(logrus.DebugLevel)
	case "trace", "TRACE":
		logrus.SetLevel(logrus.TraceLevel)
	default:
		logrus.SetLevel(logrus.InfoLevel)
	}

	// Display the welcome figlet
	fmt.Printf("%s\n", figlet)
	fmt.Println("The PeerChat Application is starting.")
	fmt.Println("This may take upto 30 seconds.")
	fmt.Println()

	customListenAddrs := []string{}
	customBootstrapPeerAddrs := []string{}
	customRelayAddrs := []string{}

	if *customListenAddrsStr != "" {
		customListenAddrs = strings.Split(*customListenAddrsStr, ",")
	}
	if *customBootstrapPeerAddrsStr != "" {
		customBootstrapPeerAddrs = strings.Split(*customBootstrapPeerAddrsStr, ",")
	}
	if *customRelayAddrsStr != "" {
		customRelayAddrs = strings.Split(*customRelayAddrsStr, ",")
	}

	// Create a new P2PHost
	p2phost := p2p.NewP2P(
		*useDefaultBootstrapPeers, *useDHT, *useMDNS,
		customListenAddrs, customRelayAddrs, customBootstrapPeerAddrs,
	)
	logrus.Infoln("Completed P2P Setup")

	if *useDHT {
		// Connect to peers with the chosen discovery method
		switch *discovery {
		case "announce":
			p2phost.AnnounceConnect()
		case "advertise":
			p2phost.AdvertiseConnect(*rendezvous)
		default:
			p2phost.AdvertiseConnect(*rendezvous)
		}
		logrus.Infoln("Connected to Service Peers")
	}

	// Join the chat room
	chatapp, _ := chat.JoinChatRoom(p2phost, *username, *chatroom)
	logrus.Infof("Joined the '%s' chatroom as '%s'", chatapp.RoomName, chatapp.UserName)

	// Wait for network setup to complete
	time.Sleep(time.Second * 5)

	// // Create the Chat UI
	ui := chat.NewUI(chatapp)
	// // Start the UI system
	err := ui.Run()
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatalln("UI run failed!")
	}
}
