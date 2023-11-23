package hole

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/pion/ice/v3"
	"github.com/pion/logging"
	"github.com/pion/stun/v2"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sync/atomic"
	"time"
)

const (
	defaultStunServer = "stun:stun1.l.google.com:19302"

	ufrag = "abcdefghijklmnopqrst"
	pwd   = "passwordpassword"
)

type signalHandler func(info *pairInfo)

var (
	logFactory = logging.NewDefaultLoggerFactory()
	logger     logging.LeveledLogger

	turnPattern = regexp.MustCompile(`turn://(\w+):(\w+)@(.+)`)

	gateWay    IceGateway
	gateWayMap map[string]IceGateway
)

type MsgDataCallback interface {
	//OnDataReceived(data string)
	OnDataReceivedByte(data []byte)
}

func RegisterIceCallback(callback MsgDataCallback) {
	gateWay.callback = callback
}

type IceGateway struct {
	role string // ctrl or ctrled
	room string

	callback         MsgDataCallback
	cancelConnection context.CancelFunc
	connected        atomic.Value
	agent            *ice.Agent
	peerConn         *ice.Conn
}

func init() {
	//logFactory.DefaultLogLevel = logging.LogLevelTrace
	gateWay = IceGateway{}
	gateWayMap = make(map[string]IceGateway)
	logFactory.DefaultLogLevel = logging.LogLevelDebug
	logger = logFactory.NewLogger("hole")
}

func StartConnect(nic, signalServer, stunServer, role, room string) {
	logger.Infof("StartConnect signal server is %v nic:%v stunServer:%v role:%v room:%v ",
		signalServer, nic, stunServer, role, room)

	gateWay.room = room
	gateWay.role = role

	gateWay.agent = createAgent(nic, stunServer, role)
	conn, _, err := connectSignalServer(signalServer, role, room, gateWay.agent)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	//var signalDone atomic.Value
	gateWay.connected.Store(false)
	go receiveFromSignal(conn, func(info *pairInfo) {
		if info == nil {
			if !gateWay.connected.Load().(bool) {
				cancel()
			}
			return
		}
		if len(info.CtrlAddress) > 0 && len(info.CtrledAddress) > 0 {
			var remoteCandidates []string
			switch role {
			case "ctrl":
				remoteCandidates = info.CtrledAddress
			case "ctrled":
				remoteCandidates = info.CtrlAddress
			default:
				panic("wrong role of signal response")
			}
			// got the peer's signal info
			addRemote(gateWay.agent, remoteCandidates)
			gateWay.connected.Store(true)

			//var peerConn *ice.Conn
			var connErr error
			switch role {
			case "ctrl":
				logger.Infof("dialing ...")
				gateWay.peerConn, connErr = gateWay.agent.Dial(ctx, ufrag, pwd)
			case "ctrled":
				logger.Infof("accepting...")
				gateWay.peerConn, err = gateWay.agent.Accept(ctx, ufrag, pwd)
			}
			if connErr != nil {
				panic(connErr)
			}

			gateWay.cancelConnection = cancel
			gateWayMap[room] = gateWay

			logger.Info("----------------READY-----------------")
			// write stdin to peer's output, and vice versa
			scanner := bufio.NewScanner(os.Stdin)
			go func() {
				for scanner.Scan() {
					select {
					case <-ctx.Done():
						return
					default:
					}

					text := scanner.Text()
					inp := []byte(text)
					if bytes.Compare(inp, []byte("ping")) == 0 {
						// add timestamp to ping command
						now := time.Now().UnixMilli()
						inp = append(inp, make([]byte, 8)...)
						binary.BigEndian.PutUint64(inp[4:], uint64(now))
					}

					gateWay.peerConn.Write(inp)
				}
			}()
			go func() {
				data := make([]byte, 4096)
				for {
					select {
					case <-ctx.Done():
						return
					default:
					}

					if n, err := gateWay.peerConn.Read(data); err != nil {
						logger.Errorf("read peer error:%v, n:%v", err, n)
						cancel()
					} else {
						if n == (4 + 8) {
							if bytes.Compare(data[:4], []byte("ping")) == 0 {
								// copy timestamp to pong
								pong := append([]byte("pong"), data[4:4+8]...)
								gateWay.peerConn.Write(pong)
								continue
							}
							if bytes.Compare(data[:4], []byte("pong")) == 0 {
								ts := int64(binary.BigEndian.Uint64(data[4:]))
								delay := time.Now().UnixMilli() - ts
								logger.Infof("--> PONG [%vms]", delay/2)
								continue
							}
						}

						gateWay.callback.OnDataReceivedByte(data[:n])
						//gateWay.callback.OnDataReceived(string(data[:n]))

						logger.Infof("--> %v", string(data[:n]))
					}
				}
			}()

		}

	})
	//<-ctx.Done()
}

func createAgent(nic, server, _ string) *ice.Agent {
	var username, password string
	if len(server) == 0 {
		server = defaultStunServer
	}
	if matches := turnPattern.FindStringSubmatch(server); matches != nil {
		logger.Infof("turn server credential: %v:%v", matches[1], matches[2])
		username, password = matches[1], matches[2]
		server = fmt.Sprintf("turn:%v", matches[3])
	} else {
		logger.Infof("turn server FindStringSubmatch not match:%v", server)
	}
	uri, err := stun.ParseURI(server)
	if err != nil {
		panic(err)
	}
	if uri.Scheme == stun.SchemeTypeTURN {
		if username == "" || password == "" {
			panic("must provide username/password for turn server")
		}
		uri.Username = username
		uri.Password = password
	}
	config := &ice.AgentConfig{
		Urls:                 []*stun.URI{uri},
		PortMin:              0,
		PortMax:              0,
		LocalUfrag:           ufrag,
		LocalPwd:             pwd,
		MulticastDNSMode:     0,
		MulticastDNSHostName: "",
		DisconnectedTimeout:  nil,
		FailedTimeout:        nil,
		KeepaliveInterval:    nil,
		CheckInterval:        nil,
		NetworkTypes:         []ice.NetworkType{ice.NetworkTypeUDP4},
		CandidateTypes: []ice.CandidateType{
			ice.CandidateTypeHost, ice.CandidateTypeServerReflexive,
			ice.CandidateTypePeerReflexive, ice.CandidateTypeRelay,
		},
		LoggerFactory:          logFactory,
		MaxBindingRequests:     nil,
		Lite:                   false,
		NAT1To1IPCandidateType: 0,
		NAT1To1IPs:             nil,
		HostAcceptanceMinWait:  nil,
		SrflxAcceptanceMinWait: nil,
		PrflxAcceptanceMinWait: nil,
		RelayAcceptanceMinWait: nil,
		Net:                    nil,
		InterfaceFilter: func(s string) bool {
			if len(nic) == 0 {
				// no specify any card, allow all
				return true
			}
			if nic == s {
				return true
			}
			return false
		},
		IPFilter:           nil,
		InsecureSkipVerify: false,
		TCPMux:             nil,
		UDPMux:             nil,
		UDPMuxSrflx:        nil,
		ProxyDialer:        nil,
		IncludeLoopback:    false,
		TCPPriorityOffset:  nil,
		DisableActiveTCP:   false,
	}

	agent, err := ice.NewAgent(config)
	if err != nil {
		logger.Error("ice.NewAgent fail ...")
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	agent.OnCandidate(func(c ice.Candidate) {
		if c == nil {
			return
		}
		logger.Info(c.String())
		if c.Type() == ice.CandidateTypeRelay {
			cancel()
		}
	})
	agent.GatherCandidates()
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
		panic("no relay candidate gathered")
	}

	agent.OnSelectedCandidatePairChange(func(local ice.Candidate, remote ice.Candidate) {
		logger.Infof("selected candidate pair: local(%v) <-> remote(%v)", local, remote)
	})

	return agent
}

func receiveFromSignal(conn *websocket.Conn, handler signalHandler) {
	for {
		var pi pairInfo
		err := conn.ReadJSON(&pi)
		if err != nil {
			logger.Infof("websocket read error:%v, exit", err)
			handler(nil)
			return
		}
		logger.Infof("websocket read data: %v", pi)
		handler(&pi)
	}

}

func connectSignalServer(host, role, roomName string, agent *ice.Agent) (*websocket.Conn, *http.Response, error) {
	qvs := url.Values{}
	qvs.Add("role", role)
	qvs.Add("room", roomName)
	if candidates, err := agent.GetLocalCandidates(); err != nil {
		panic(err)
	} else {
		for _, c := range candidates {
			// for cross-nats testing purpose, remove host candidates
			if c.Type() == ice.CandidateTypeServerReflexive || c.Type() == ice.CandidateTypeRelay {
				logger.Infof("send to signal local candidate: %v", c.Marshal())
				qvs.Add("addr", c.Marshal())
			}
		}
	}
	qs := qvs.Encode()
	u := url.URL{Scheme: "ws", Host: host, Path: "/room", RawQuery: qs}
	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 2 * time.Second
	return dialer.Dial(u.String(), nil)
}

func addRemote(a *ice.Agent, remoteCandidates []string) {
	for _, c := range remoteCandidates {
		if candidate, err := ice.UnmarshalCandidate(c); err != nil {
			panic(err)
		} else {
			logger.Infof("add remote candidate: %v", candidate)
			a.AddRemoteCandidate(candidate)
		}
	}
}

func StartSendMsg(room string, text string) {
	value, ok := gateWayMap[room]
	if !ok {
		logger.Infof("StartSendMsg not find room:%v text:%v ", room, text)
		return
	}

	logger.Infof("StartSendMsg find room:%v text:%v ", room, text)
	inp := []byte(text)
	if bytes.Compare(inp, []byte("ping")) == 0 {
		// add timestamp to ping command
		now := time.Now().UnixMilli()
		inp = append(inp, make([]byte, 8)...)
		binary.BigEndian.PutUint64(inp[4:], uint64(now))
	}

	value.peerConn.Write(inp)

}

/*func StartSendH264Byte(room string, data []byte) {
	if data == nil {
		logger.Errorf("StartSendH264Byte data nil")
		return
	}
	value, ok := gateWayMap[room]
	if !ok {
		logger.Errorf("StartSendH264Byte not find room:%v", room)
		return
	}

	inp := data
	value.peerConn.Write(inp)

}*/

func StartSendH264Byte(room string, data []byte) {
	if data == nil {
		logger.Errorf("StartSendH264Byte data nil")
		return
	}

	value, ok := gateWayMap[room]
	if !ok {
		logger.Errorf("StartSendH264Byte not find room:%v", room)
		return
	}

	inp := bytes.NewBuffer(data)
	buffer := make([]byte, 1024)
	for {
		n, err := inp.Read(buffer)
		if err != nil && err != io.EOF {
			logger.Errorf("StartSendH264Byte error reading input data: %v", err)
			break
		}
		if err == io.EOF {
			break
		}
		if n == 0 {
			break
		}
		value.peerConn.Write(buffer[:n])
	}
}

func StopConnect(room string) {
	logger.Infof("StopConnect close connected...")

	value, ok := gateWayMap[room]
	if ok {
		if value.agent != nil {
			value.agent.Close()
		}
		if value.cancelConnection != nil {
			value.cancelConnection()
		}
		delete(gateWayMap, room)
	} else {
		logger.Errorf("StopConnect not find room:%v ,error", room)
	}

}

func StopAllConnect() {
	logger.Infof("StopAllConnect close connected...")

	for key, value := range gateWayMap {
		if value.agent != nil {
			value.agent.Close()
		}
		if value.cancelConnection != nil {
			value.cancelConnection()
		}
		delete(gateWayMap, key)
	}
	gateWayMap = nil
}

func IsRoomConnected(room string) bool {
	_, ok := gateWayMap[room]
	if !ok {
		logger.Errorf("IsRoomConnected not find room:%v", room)
		return false
	}
	logger.Infof("IsRoomConnected find room:%v", room)
	return true
}
