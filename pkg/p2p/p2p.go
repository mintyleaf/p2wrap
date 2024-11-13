package p2p

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"strings"
	"sync"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/config"
	"github.com/libp2p/go-libp2p/core/crypto"
	host "github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	discoveryRouting "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	yamux "github.com/libp2p/go-libp2p/p2p/muxer/yamux"
	connmgr "github.com/libp2p/go-libp2p/p2p/net/connmgr"
	tls "github.com/libp2p/go-libp2p/p2p/security/tls"
	"github.com/mr-tron/base58/base58"
	"github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multihash"
	"github.com/sirupsen/logrus"
)

const Service = "p2p4ai/peerchat"

// A structure that represents a P2P Host
type P2P struct {
	// Represents the host context layer
	Ctx context.Context

	// Represents the libp2p host
	Host host.Host

	// Represents the DHT routing table
	KadDHT *dht.IpfsDHT

	// Represents the peer discovery service
	Discovery *discoveryRouting.RoutingDiscovery

	// Represents the PubSub Handler
	PubSub *pubsub.PubSub
}

// discoveryMDNSNotifee gets notified when we find a new peer via mDNS discovery
type discoveryMDNSNotifee struct {
	h host.Host
}

// HandlePeerFound connects to peers discovered via mDNS. Once they're connected,
// the PubSub system will automatically start interacting with them if they also
// support PubSub.
func (n *discoveryMDNSNotifee) HandlePeerFound(pi peer.AddrInfo) {
	err := n.h.Connect(context.Background(), pi)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Traceln("Failed connect to MDNS peer")
	}
}

// setupMDNSDiscovery creates an mDNS discovery service and attaches it to the libp2p Host.
// This lets us automatically discover peers on the same LAN and connect to them.
func setupMDNSDiscovery(h host.Host) error {
	// setup mDNS discovery to find local peers
	s := mdns.NewMdnsService(h, Service, &discoveryMDNSNotifee{h: h})
	return s.Start()
}

/*
A constructor function that generates and returns a P2P object.

Constructs a libp2p host with TLS encrypted secure transportation that works over a TCP
transport connection using a Yamux Stream Multiplexer and uses UPnP for the NAT traversal.

A Kademlia DHT is then bootstrapped on this host using the default peers offered by libp2p
and a Peer Discovery service is created from this Kademlia DHT. The PubSub handler is then
created on the host using the peer discovery service created prior.
*/
func NewP2P(useDefaultPeers, useDHT, useMDNS bool, customListenAddrs, customBootstrapPeerAddrs string) *P2P {
	opts := []config.Option{}

	// Setup a background context
	ctx := context.Background()

	// Set up the host identity options
	prvkey, _, err := crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, rand.Reader)
	identity := libp2p.Identity(prvkey)
	// Handle any potential error
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatalln("Failed to Generate P2P Identity Configuration!")
	}
	opts = append(opts, identity)

	// Trace log
	logrus.Traceln("Generated P2P Identity Configuration")

	// Set up TLS security and provide default transports
	opts = append(opts, libp2p.Security(tls.ID, tls.New))
	opts = append(opts, libp2p.DefaultTransports)

	// Trace log
	logrus.Traceln("Generated P2P Security and Transport Configurations")

	addrs := []string{}
	if customListenAddrs != "" {
		addrs = append(addrs, strings.Split(customListenAddrs, ",")...)
	} else {
		addrs = append(addrs, []string{
			"/ip4/0.0.0.0/tcp/0",
			"/ip4/0.0.0.0/udp/0/quic-v1",
			"/ip4/0.0.0.0/udp/0/quic-v1/webtransport",
			"/ip4/0.0.0.0/udp/0/webrtc-direct",
			"/ip6/::/tcp/0",
			"/ip6/::/udp/0/quic-v1",
			"/ip6/::/udp/0/quic-v1/webtransport",
			"/ip6/::/udp/0/webrtc-direct",
		}...)
	}
	listenAddrs := make([]multiaddr.Multiaddr, 0, len(addrs))
	for _, s := range addrs {
		addr, err := multiaddr.NewMultiaddr(s)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error":            err.Error(),
				"multiaddr_string": s,
			}).Fatalln("Multiaddr creation failed")
		}
		listenAddrs = append(listenAddrs, addr)
	}
	// Set up host listener address options
	listen := libp2p.ListenAddrs(listenAddrs...)
	// Handle any potential error
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatalln("Failed to Generate P2P Address Listener Configuration!")
	}
	opts = append(opts, listen)

	// Trace log
	logrus.Traceln("Generated P2P Address Listener Configuration")

	// Set up the stream multiplexer and connection manager options
	muxer := libp2p.Muxer("/yamux/1.0.0", yamux.DefaultTransport)
	opts = append(opts, muxer)

	connManager, err := connmgr.NewConnManager(
		100, // Lowwater
		400, // HighWater,
		connmgr.WithGracePeriod(time.Minute),
	)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatalln("Faied to create new connection manager!")
	}
	conn := libp2p.ConnectionManager(connManager)
	opts = append(opts, conn)

	// Trace log
	logrus.Traceln("Generated P2P Stream Multiplexer, Connection Manager Configurations")

	// Setup NAT traversal and relay options
	nat := libp2p.NATPortMap()
	opts = append(opts, nat)

	// TODO setup relay
	// relay := libp2p.EnableAutoRelay()

	// Trace log
	logrus.Traceln("Generated P2P NAT Traversal and Relay Configurations")

	// Declare a KadDHT
	var kaddht *dht.IpfsDHT
	if useDHT {
		// Setup a routing configuration with the KadDHT
		routing := libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
			kaddht = setupKadDHT(ctx, h)
			return kaddht, err
		})
		opts = append(opts, routing)
	}

	// Trace log
	logrus.Traceln("Generated P2P Routing Configurations")

	// Construct a new libP2P host with the created options
	nodehost, err := libp2p.New(opts...)
	// Handle any potential error
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatalln("Failed to Create the P2P Host!")
	}

	// Debug log
	logrus.Debugln("Created the P2P Host")

	pubsubOpts := []pubsub.Option{}
	routingDiscovery := &discoveryRouting.RoutingDiscovery{}
	if useDHT {
		addrs = []string{}
		useDefaultPeers = customBootstrapPeerAddrs == "" || (useDefaultPeers && customBootstrapPeerAddrs != "")
		if customBootstrapPeerAddrs != "" {
			addrs = append(addrs, strings.Split(customBootstrapPeerAddrs, ",")...)
		}
		length := len(addrs)
		if useDefaultPeers {
			length += len(dht.DefaultBootstrapPeers)
		}
		bootstrapPeerAddrs := make([]multiaddr.Multiaddr, 0, length)
		for _, s := range addrs {
			addr, err := multiaddr.NewMultiaddr(s)
			if err != nil {
				logrus.WithFields(logrus.Fields{
					"error":            err.Error(),
					"multiaddr_string": s,
				}).Fatalln("Multiaddr creation failed!")
			}
			bootstrapPeerAddrs = append(bootstrapPeerAddrs, addr)
		}
		if useDefaultPeers {
			bootstrapPeerAddrs = append(bootstrapPeerAddrs, dht.DefaultBootstrapPeers...)
		}
		// Bootstrap the Kad DHT
		bootstrapDHT(ctx, nodehost, kaddht, bootstrapPeerAddrs)
		// Debug log
		logrus.Debugln("Bootstrapped the Kademlia DHT and Connected to Bootstrap Peers")

		// Create a peer discovery service using the Kad DHT
		routingDiscovery = discoveryRouting.NewRoutingDiscovery(kaddht)
		pubsubOpts = append(pubsubOpts, pubsub.WithDiscovery(routingDiscovery))
		// Debug log
		logrus.Debugln("Created the Peer Discovery Service")
	}
	if useMDNS {
		if err := setupMDNSDiscovery(nodehost); err != nil {
			logrus.WithFields(logrus.Fields{
				"error": err.Error(),
			}).Fatalln("Failed to setup MDNS discovery!")
		}
		logrus.Debugln("Created the MDNS Discovery Service")
	}

	// Create a PubSub handler with the routing discovery
	pubsubhandler, err := pubsub.NewGossipSub(ctx, nodehost, pubsubOpts...)
	// Handle any potential error
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
			"type":  "GossipSub",
		}).Fatalln("PubSub Handler Creation Failed!")
	}
	// Debug log
	logrus.Debugln("Created the PubSub Handler")

	// Return the P2P object
	return &P2P{
		Ctx:       ctx,
		Host:      nodehost,
		KadDHT:    kaddht,
		Discovery: routingDiscovery,
		PubSub:    pubsubhandler,
	}
}

// A method of P2P to connect to service peers.
// This method uses the Advertise() functionality of the Peer Discovery Service
// to advertise the service and then disovers all peers advertising the same.
// The peer discovery is handled by a go-routine that will read from a channel
// of peer address information until the peer channel closes
func (p2p *P2P) AdvertiseConnect(rendezvous string) {
	// Advertise the availabilty of the service on this node
	ttl, err := p2p.Discovery.Advertise(p2p.Ctx, rendezvous)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatalln("P2P Advertise failed!")
	}
	// Debug log
	logrus.Debugln("Advertised the PeerChat Service.")
	// Sleep to give time for the advertisment to propogate
	time.Sleep(time.Second * 5)
	// Debug log
	logrus.Debugf("Service Time-to-Live is %s", ttl)

	// Find all peers advertising the same service
	peerchan, err := p2p.Discovery.FindPeers(p2p.Ctx, Service)
	// Handle any potential error
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatalln("P2P Peer Discovery Failed!")
	}
	// Trace log
	logrus.Traceln("Discovered PeerChat Service Peers.")

	// Connect to peers as they are discovered
	go handlePeerDiscovery(p2p.Host, peerchan)
	// Trace log
	logrus.Traceln("Started Peer Connection Handler.")
}

// A method of P2P to connect to service peers.
// This method uses the Provide() functionality of the Kademlia DHT directly to announce
// the ability to provide the service and then discovers all peers that provide the same.
// The peer discovery is handled by a go-routine that will read from a channel
// of peer address information until the peer channel closes
func (p2p *P2P) AnnounceConnect() {
	// Generate the Service CID
	cidvalue := generateCID(Service)
	// Trace log
	logrus.Traceln("Generated the Service CID.")

	// Announce that this host can provide the service CID
	err := p2p.KadDHT.Provide(p2p.Ctx, cidvalue, true)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatalln("Failed to Announce Service CID!")
	}
	// Debug log
	logrus.Debugln("Announced the PeerChat Service.")
	// Sleep to give time for the advertisment to propogate
	time.Sleep(time.Second * 5)

	// Find the other providers for the service CID
	peerchan := p2p.KadDHT.FindProvidersAsync(p2p.Ctx, cidvalue, 0)
	// Trace log
	logrus.Traceln("Discovered PeerChat Service Peers.")

	// Connect to peers as they are discovered
	go handlePeerDiscovery(p2p.Host, peerchan)
	// Debug log
	logrus.Debugln("Started Peer Connection Handler.")
}

// A function that generates a Kademlia DHT object and returns it
func setupKadDHT(ctx context.Context, nodehost host.Host) *dht.IpfsDHT {
	// Create DHT server mode option
	dhtmode := dht.Mode(dht.ModeServer)
	// Rertieve the list of boostrap peer addresses
	bootstrappeers := dht.GetDefaultBootstrapPeerAddrInfos()
	// Create the DHT bootstrap peers option
	dhtpeers := dht.BootstrapPeers(bootstrappeers...)

	// Trace log
	logrus.Traceln("Generated DHT Configuration.")

	// Start a Kademlia DHT on the host in server mode
	kaddht, err := dht.New(ctx, nodehost, dhtmode, dhtpeers)
	// Handle any potential error
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatalln("Failed to Create the Kademlia DHT!")
	}

	// Return the KadDHT
	return kaddht
}

// A function that bootstraps a given Kademlia DHT to satisfy the IPFS router
// interface and connects to all the bootstrap peers provided by libp2p
func bootstrapDHT(ctx context.Context, nodehost host.Host, kaddht *dht.IpfsDHT, bootstrapPeers []multiaddr.Multiaddr) {
	// Bootstrap the DHT to satisfy the IPFS Router interface
	if err := kaddht.Bootstrap(ctx); err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatalln("Failed to Bootstrap the Kademlia!")
	}

	// Trace log
	logrus.Traceln("Set the Kademlia DHT into Bootstrap Mode.")

	// Declare a WaitGroup
	var wg sync.WaitGroup
	// Declare counters for the number of bootstrap peers
	var connectedbootpeers int
	var totalbootpeers int

	// Iterate over the default bootstrap peers provided by libp2p
	for _, peeraddr := range bootstrapPeers {
		// Retrieve the peer address information
		peerinfo, _ := peer.AddrInfoFromP2pAddr(peeraddr)

		// Incremenent waitgroup counter
		wg.Add(1)
		// Start a goroutine to connect to each bootstrap peer
		go func() {
			// Defer the waitgroup decrement
			defer wg.Done()
			// Attempt to connect to the bootstrap peer
			if err := nodehost.Connect(ctx, *peerinfo); err != nil {
				// Increment the total bootstrap peer count
				totalbootpeers++
			} else {
				// Increment the connected bootstrap peer count
				connectedbootpeers++
				// Increment the total bootstrap peer count
				totalbootpeers++
			}
		}()
	}

	// Wait for the waitgroup to complete
	wg.Wait()

	// Log the number of bootstrap peers connected
	logrus.Debugf("Connected to %d out of %d Bootstrap Peers.", connectedbootpeers, totalbootpeers)
}

// A function that connects the given host to all peers recieved from a
// channel of peer address information. Meant to be started as a go routine.
func handlePeerDiscovery(nodehost host.Host, peerchan <-chan peer.AddrInfo) {
	// Iterate over the peer channel
	for peer := range peerchan {
		// Ignore if the discovered peer is the host itself
		if peer.ID == nodehost.ID() {
			continue
		}

		// Connect to the peer
		err := nodehost.Connect(context.Background(), peer)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error": err.Error(),
			}).Traceln("Failed to connect to peer!")
		}
	}
}

// A function that generates a CID object for a given string and returns it.
// Uses SHA256 to hash the string and generate a multihash from it.
// The mulithash is then base58 encoded and then used to create the CID
func generateCID(namestring string) cid.Cid {
	// Hash the service content ID with SHA256
	hash := sha256.Sum256([]byte(namestring))
	// Append the hash with the hashing codec ID for SHA2-256 (0x12),
	// the digest size (0x20) and the hash of the service content ID
	finalhash := append([]byte{0x12, 0x20}, hash[:]...)
	// Encode the fullhash to Base58
	b58string := base58.Encode(finalhash)

	// Generate a Multihash from the base58 string
	mulhash, err := multihash.FromB58String(string(b58string))
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatalln("Failed to Generate Service CID!")
	}

	// Generate a CID from the Multihash
	cidvalue := cid.NewCidV1(12, mulhash)
	// Return the CID
	return cidvalue
}
