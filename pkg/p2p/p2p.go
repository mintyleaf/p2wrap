package p2p

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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

// MDNS stuff
//
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
func NewP2P(
	useDefaultPeers, useDHT, useMDNS bool,
	customListenAddrs, customRelayAddrs, customBootstrapPeerAddrs []string,
) *P2P {
	ctx := context.Background()
	opts := []config.Option{}

	// Setup private key for Identity option
	prvkey, _, err := crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, rand.Reader)
	identity := libp2p.Identity(prvkey)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatalln("Failed to Generate P2P Identity Configuration!")
	}
	opts = append(opts, identity)
	logrus.Traceln("Generated P2P Identity Configuration")

	// Add tls security and default transports options
	opts = append(opts, libp2p.Security(tls.ID, tls.New))
	opts = append(opts, libp2p.DefaultTransports)
	logrus.Traceln("Generated P2P Security and Transport Configurations")

	// Generate list of default or custom listen addresses
	// (possibly can be combined through useDefaultListenAddrs if needed in future)
	listen := libp2p.ListenAddrs(getListenAddresses(customListenAddrs)...)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatalln("Failed to Generate P2P Address Listener Configuration!")
	}
	opts = append(opts, listen)
	logrus.Traceln("Generated P2P Address Listener Configuration")

	// Add stream muxer option
	muxer := libp2p.Muxer("/yamux/1.0.0", yamux.DefaultTransport)
	opts = append(opts, muxer)

	// Add connmanager option
	connManager, err := connmgr.NewConnManager(
		100, // Lowwater
		400, // HighWater,
		connmgr.WithGracePeriod(time.Minute),
	)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatalln("Failed to create new connection manager!")
	}
	conn := libp2p.ConnectionManager(connManager)
	opts = append(opts, conn)
	logrus.Traceln("Generated P2P Stream Multiplexer, Connection Manager Configurations")

	// Add NAT traversal option
	opts = append(opts, libp2p.NATPortMap())
	opts = append(opts, libp2p.EnableAutoNATv2())
	logrus.Traceln("Generated NAT traversal options")

	if len(customRelayAddrs) > 0 {
		relayAddrInfos := getRelayAddrs(customRelayAddrs)
		opts = append(opts, libp2p.EnableRelay())
		opts = append(opts, libp2p.EnableAutoRelayWithStaticRelays(relayAddrInfos))
		logrus.Traceln("Added static relays")
	}

	// We need to have empty objects for DHT, since we need to add it as option
	// and then to do the boostrap loop after the main p2p host creation
	kaddht := &dht.IpfsDHT{}
	bootstrapPeerMultiaddrs := []multiaddr.Multiaddr{}
	bootstrapPeerAddrInfos := []peer.AddrInfo{}

	if useDHT {
		bootstrapPeerMultiaddrs, bootstrapPeerAddrInfos = getBootstrapAddrs(customBootstrapPeerAddrs, useDefaultPeers)

		// Setup a routing configuration with the KadDHT
		routing := libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
			kaddht = setupKadDHT(ctx, h, bootstrapPeerAddrInfos)
			return kaddht, err
		})
		opts = append(opts, routing)
		logrus.Traceln("Added routing with Kad DHT option")
	}

	// Construct a new libP2P host with the created options
	nodehost, err := libp2p.New(opts...)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatalln("Failed to Create the P2P Host!")
	}
	logrus.Debugln("Created the P2P Host")

	pubsubOpts := []pubsub.Option{}
	routingDiscovery := &discoveryRouting.RoutingDiscovery{}
	if useDHT {
		// Bootstrap the Kad DHT
		bootstrapDHT(ctx, nodehost, kaddht, bootstrapPeerMultiaddrs)
		logrus.Debugln("Bootstrapped the Kademlia DHT and Connected to Bootstrap Peers")

		// Create a peer discovery service using the Kad DHT
		routingDiscovery = discoveryRouting.NewRoutingDiscovery(kaddht)
		pubsubOpts = append(pubsubOpts, pubsub.WithDiscovery(routingDiscovery))
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
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err.Error(),
			"type":  "GossipSub",
		}).Fatalln("PubSub Handler Creation Failed!")
	}
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
	go handlePeerDHTDiscovery(p2p.Host, peerchan)
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
	go handlePeerDHTDiscovery(p2p.Host, peerchan)
	// Debug log
	logrus.Debugln("Started Peer Connection Handler.")
}

// A function that generates default or custom multiaddrs from their strings
func getListenAddresses(customListenAddrs []string) []multiaddr.Multiaddr {
	addrs := []string{}
	if len(customListenAddrs) > 0 {
		addrs = append(addrs, customListenAddrs...)
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
	return listenAddrs
}

// A function that generates list of bootstrap nodes with or without default ones if custom is provided
func getBootstrapAddrs(customBootstrapPeerAddrs []string, useDefaultPeers bool) ([]multiaddr.Multiaddr, []peer.AddrInfo) {
	addrs := []string{}
	useDefaultPeers = len(customBootstrapPeerAddrs) == 0 || (useDefaultPeers && len(customBootstrapPeerAddrs) > 0)
	if len(customBootstrapPeerAddrs) > 0 {
		addrs = append(addrs, customBootstrapPeerAddrs...)
	}
	length := len(addrs)
	if useDefaultPeers {
		length += len(dht.DefaultBootstrapPeers)
	}
	bootstrapPeerMultiaddrs := make([]multiaddr.Multiaddr, 0, length)
	for _, s := range addrs {
		addr, err := multiaddr.NewMultiaddr(s)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error":            err.Error(),
				"multiaddr_string": s,
			}).Fatalln("Multiaddr creation failed!")
		}
		bootstrapPeerMultiaddrs = append(bootstrapPeerMultiaddrs, addr)
	}
	if useDefaultPeers {
		bootstrapPeerMultiaddrs = append(bootstrapPeerMultiaddrs, dht.DefaultBootstrapPeers...)
	}

	bootstrapPeerAddrInfos := make([]peer.AddrInfo, 0, len(bootstrapPeerMultiaddrs))

	for _, maddr := range bootstrapPeerMultiaddrs {
		info, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error":            err.Error(),
				"multiaddr_string": maddr.String(),
			}).Fatalln("AddrInfo from multiaddr failed!")
			continue
		}
		bootstrapPeerAddrInfos = append(bootstrapPeerAddrInfos, *info)
	}
	return bootstrapPeerMultiaddrs, bootstrapPeerAddrInfos
}

// A function that generates AddrInfo's from relay addrs strings
func getRelayAddrs(customRelayAddrs []string) []peer.AddrInfo {
	relayAddrInfos := make([]peer.AddrInfo, len(customRelayAddrs))
	for _, addr := range customRelayAddrs {
		addrInfo, err := peer.AddrInfoFromString(addr)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error":            err.Error(),
				"multiaddr_string": addr,
			}).Fatalln("AddrInfo from string failed!")
			continue
		}
		relayAddrInfos = append(relayAddrInfos, *addrInfo)
	}
	return relayAddrInfos
}

// A function that generates a Kademlia DHT object and returns it
func setupKadDHT(ctx context.Context, nodehost host.Host, bootstrappeers []peer.AddrInfo) *dht.IpfsDHT {
	// Create DHT server mode option
	dhtmode := dht.Mode(dht.ModeServer)
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
func handlePeerDHTDiscovery(nodehost host.Host, peerchan <-chan peer.AddrInfo) {
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
