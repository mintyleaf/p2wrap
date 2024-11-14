# p2wrap
wrap around p2p pubsub with different configurations and with working examples

## contents
there are two examples built in:
 - peerchat fork in `cmd/chat/chat.go`
 - simple topicsub example in `cmd/topicsub/topicsub.go`

## peerchat
```
Usage of peerchat:
  -bootstrap_peers string
    	comma separated list of bootstrap peers
  -default_bootstrap_peers
    	use default bootstrap peers (if custom peers is set - add default to the bottom of the list) (default true)
  -dht
    	use dht (default true)
  -discover string
    	method to use for discovery. (default "advertise")
  -listen_addrs string
    	comma separated list of listen maddrs. fallback to libp2p defaults if empty
  -log string
    	level of logs to print.
  -mdns
    	use mdns (default true)
  -relay_addrs string
    	comma separated list of static relay maddrs
  -rendezvous string
    	rendezvous string. used if discovery is set to advertise (default "p2p4ai/peerchat")
  -room string
    	chatroom to join.
  -user string
    	username to use in the chatroom.
```

## topicsub
```
Usage of topicsub:
  -bootstrap_peers string
    	comma separated list of bootstrap peers
  -default_bootstrap_peers
    	use default bootstrap peers (if custom peers is set - add default to the bottom of the list) (default true)
  -dht
    	use dht (default true)
  -discover string
    	method to use for discovery. (default "advertise")
  -listen_addrs string
    	comma separated list of listen maddrs. fallback to libp2p defaults if empty
  -mdns
    	use mdns (default true)
  -relay_addrs string
    	comma separated list of static relay maddrs
  -rendezvous string
    	rendezvous string. used if discovery is set to advertise (default "p2p4ai/peerchat")
  -topic string
    	PubSub topic name (default "p2p4ai/peerchat")
```

## build/run warning
if you using go 1.23+ you can face various linker issues because of packages p2p lib is using  
to fix it - pass `-ldflags="-checklinkname=0"` to the run/build command.

## run example
first pane, run the chat demo with fast mDNS only resolving  
`go run cmd/chat/chat.go -user username -room test -log trace -mdns=true -dht=false`

second pane, run topicsub demo with fast mDNS only resolving  
`go run cmd/topicsub/topicsub.go -mdns=true -dht=false -topic room-peerchat-test`

now you can sniff the message data from the topic "room-peerchat-test", since the peerchat formats the "room" flag with room-peerchat-%s as PubSub topic name  

## credits
[Based on this manishmeganathan work](https://github.com/manishmeganathan/peerchat)
