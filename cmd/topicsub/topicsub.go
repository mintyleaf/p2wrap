package main

import (
	"context"
	"flag"
	"log"
	"os"
	"p2p4ai/pkg/p2p"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// Setup logrus, since p2p part already using it
// TODO get rid off that useless piece of bloatware for uber or idk and use default go logger
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
	logrus.SetLevel(logrus.TraceLevel)
}

func main() {
	// flags
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
	topicStr := flag.String("topic", p2p.Service, "PubSub topic name")
	flag.Parse()

	// parse addrs, if any
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

	// create a new P2PHost
	p2phost := p2p.NewP2P(
		*useDefaultBootstrapPeers, *useDHT, *useMDNS,
		customListenAddrs, customRelayAddrs, customBootstrapPeerAddrs,
	)
	log.Println("Completed P2P Setup")

	if *useDHT {
		// connect to peers with the chosen discovery method
		switch *discovery {
		case "announce":
			p2phost.AnnounceConnect()
		case "advertise":
			p2phost.AdvertiseConnect(*rendezvous)
		default:
			p2phost.AdvertiseConnect(*rendezvous)
		}
		log.Println("Connected to Service Peers")
	}

	// Create a PubSub topic with the room name
	topic, err := p2phost.PubSub.Join(*topicStr)
	// Check the error
	if err != nil {
		log.Fatalf("Failed to join to topic \"%s\", %v\n", *topicStr, err)
	}

	// Subscribe to the PubSub topic
	sub, err := topic.Subscribe()
	// Check the error
	if err != nil {
		log.Fatalf("Failed to subscribe to topic \"%s\", %v\n", *topicStr, err)
	}

	// Listen messages from the topic forever
	ctx := context.Background()
	for {
		message, err := sub.Next(ctx)
		if err != nil {
			log.Fatalf("Failed to get next message from topic %v\n", err)
		}

		log.Printf("%+v\n", message)
	}
}
