package hole

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/klauspost/reedsolomon"
	"github.com/pion/ice/v3"
	"github.com/pion/logging"
	"github.com/pion/stun/v2"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultStunServer = "stun:stun1.l.google.com:19302"
	ufrag             = "abcdefghijklmnopqrst"
	pwd               = "passwordpassword"
)

type signalHandler func(info *pairInfo)

var GlobalFecConfig bool
var MyDataShards = 3
var MyParityShards = 2

var (
	logFactory = logging.NewDefaultLoggerFactory()
	logger     logging.LeveledLogger

	turnPattern = regexp.MustCompile(`turn://(\w+):(\w+)@(.+)`)

	gateWay    IceGateway
	gateWayMap map[string]IceGateway

	roomDataLock sync.Mutex

	tempData []byte

	packetSeqNum = uint32(1)
)

type MsgDataCallback interface {
	//OnDataReceived(data string,room string))
	OnDataReceivedByte(data []byte, room string)
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
	//listener         *kcp.Listener

	//Encode reedsolomon.Encoder

	//	peerConn1 *kcp.UDPSession
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

	//var externPort, externIp string

	gateWay.room = room
	gateWay.role = role
	//	gateWay.Encode, _ = reedsolomon.New(MyDataShards, MyParityShards)

	gateWay.agent = createAgent(nic, stunServer, role)
	conn, _, err := connectSignalServer(signalServer, role, room, gateWay.agent)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	fmt.Printf("ctx:%v\n", ctx)
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
			fmt.Printf("info ctrl:%v ctrled:%v", info.CtrlAddress, info.CtrledAddress)
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

			//	var peerConn *ice.Conn
			var connErr error
			switch role {
			case "ctrl":
				logger.Infof("dialing:%v ...", info.CtrledAddress[0])
				gateWay.peerConn, connErr = gateWay.agent.Dial(ctx, ufrag, pwd)
			case "ctrled":
				logger.Infof("accepting:%v...", info.CtrlAddress[0])
				gateWay.peerConn, err = gateWay.agent.Accept(ctx, ufrag, pwd)
			}

			logger.Infof("candidate size is:%v", len(remoteCandidates))

			if connErr != nil {
				panic(connErr)
			}

			gateWay.cancelConnection = cancel
			gateWayMap[room] = gateWay
			go SendBufferedData()

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

						gateWay.callback.OnDataReceivedByte(data[:n], room)
						//gateWay.callback.OnDataReceived(string(data[:n]))

						//logger.Infof("--> %v", string(data[:n]))
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

func StartSendH264ByteAll(room string, data []byte) {
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

}

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

	if value.peerConn == nil {
		logger.Errorf("peerConn nil")
		return
	}

	inp := bytes.NewBuffer(data)
	buffer := make([]byte, 1200)

	for {
		n, err := inp.Read(buffer)
		if err != nil && err != io.EOF {
			logger.Errorf("StartSendH264Byte error reading input data: %v", err)
			return
		}
		if err == io.EOF {
			return
		}
		if n == 0 {
			return
		}
		if GlobalFecConfig {
			tempData = append(tempData, buffer[:n]...)
			encodeData, err := Encode(tempData)
			if err != nil {
				logger.Errorf("fec encode data fail...")
				//encodeData = buffer[:n]
			}
			seqNum := 0 // 分包序号
			// 分片发送
			for _, shard := range encodeData {
				// 在每个分片的开头添加序号
				packetInfo := make([]byte, 8)
				binary.LittleEndian.PutUint32(packetInfo[:4], packetSeqNum)
				binary.LittleEndian.PutUint32(packetInfo[4:8], uint32(seqNum))
				shardWithSeq := append(packetInfo, shard...)

				//	fmt.Println("write it len", len(shardWithSeq), len(encodedData))
				value.peerConn.Write(shardWithSeq)
				seqNum++
			}
			packetSeqNum++
			tempData = nil
		} else {
			packetInfo := make([]byte, 4)
			binary.LittleEndian.PutUint32(packetInfo[:4], packetSeqNum)
			shardWithSeq := append(packetInfo, buffer[:n]...)
			value.peerConn.Write(shardWithSeq)
			packetSeqNum++
			//value.peerConn.Write(buffer[:n])
		}
	}
}

// StartReceivedData OnDataReceivedByte
func StartReceivedData(room string) {
	value, ok := gateWayMap[room]
	if !ok {
		logger.Errorf("not find room:%v", room)
		return
	}

	go func() {
		data := make([]byte, 4096)
		for {
			if n, err := value.peerConn.Read(data); err != nil {
				logger.Errorf("read peer error:%v, n:%v", err, n)
				return
			} else {
				value.callback.OnDataReceivedByte(data[:n], room)
			}
		}
	}()
}

type BufferedData struct {
	Room  string
	Data  []byte
	mutex sync.Mutex
}

var bufferedDataMap = make(map[string]*BufferedData)

func SetFecEnable(enabled bool) {
	GlobalFecConfig = enabled
}

func StartSendH264ByteQueue(room string, data []byte) {
	if data == nil {
		logger.Errorf("StartSendH264Byte data nil")
		return
	}

	_, ok := gateWayMap[room]
	if !ok {
		logger.Errorf("StartSendH264Byte not find room:%v", room)
		return
	}

	bufferedData, exists := bufferedDataMap[room]
	if !exists {
		bufferedData = &BufferedData{
			Room: room,
		}
		bufferedDataMap[room] = bufferedData
	}
	bufferedData.mutex.Lock()
	defer bufferedData.mutex.Unlock()
	bufferedData.Data = append(bufferedData.Data, data...)

}

func SendBufferedData() {
	ticker := time.Tick(time.Microsecond)
	for {
		select {
		case <-ticker:
			for room, bufferedData := range bufferedDataMap {

				for len(bufferedData.Data) >= 1200 {
					bufferedData.mutex.Lock()
					tempData = append(tempData, bufferedData.Data[:1200]...)
					bufferedData.Data = bufferedData.Data[1200:]
					bufferedData.mutex.Unlock()
					//	fp1.Write(tempData)
					value, ok := gateWayMap[room]
					if !ok {
						continue
					}

					if GlobalFecConfig {
						encodeData, err := Encode(tempData)
						if err != nil {
							logger.Errorf("fec encode data fail...")
						}

						seqNum := 0 // 分包序号
						// 分片发送
						for _, shard := range encodeData {
							// 在每个分片的开头添加序号
							packetInfo := make([]byte, 8)
							binary.LittleEndian.PutUint32(packetInfo[:4], packetSeqNum)
							binary.LittleEndian.PutUint32(packetInfo[4:8], uint32(seqNum))
							shardWithSeq := append(packetInfo, shard...)

							value.peerConn.Write(shardWithSeq)
							seqNum++

						}
						packetSeqNum++

					} else {
						packetInfo := make([]byte, 4)
						binary.LittleEndian.PutUint32(packetInfo[:4], packetSeqNum)
						shardWithSeq := append(packetInfo, tempData...)
						value.peerConn.Write(shardWithSeq)
						packetSeqNum++
					}
					tempData = nil
				}
			}
		}
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

func SetFecParm(dataShard, parityShard int) {
	MyDataShards = dataShard
	MyParityShards = parityShard
}

func GetIceGateWay(room string) IceGateway {
	value, ok := gateWayMap[room]
	if !ok {
		logger.Errorf("IsRoomConnected not find room:%v", room)
	}
	//logger.Infof("IsRoomConnected find room:%v", room)
	return value
}

func Encode(data []byte) ([][]byte, error) {

	enc, err := reedsolomon.New(MyDataShards, MyParityShards)
	if err != nil {
		return nil, err
	}

	shards, err := enc.Split(data)
	if err != nil {
		fmt.Println("encode split fail...", err)
		return nil, err
	}

	err = enc.Encode(shards)
	if err != nil {
		fmt.Println("encode fail...", err)
		return nil, err
	}

	//fmt.Println("shards len:", len(shards))

	/*var buffer bytes.Buffer
	for _, shard := range shards {
		binary.Write(&buffer, binary.LittleEndian, int32(len(shard)))
		buffer.Write(shard)
	}*/

	return shards, nil
}

func Decode(data [][]byte) error {
	enc, err := reedsolomon.New(MyDataShards, MyParityShards)
	if err != nil {
		return err
	}

	// Verify the shards
	ok, err := enc.Verify(data)
	if ok {
		fmt.Println("No reconstruction needed")
	} else {
		fmt.Println("Verification failed. Reconstructing data")
		err = enc.Reconstruct(data)
		if err != nil {
			fmt.Println("Reconstruct failed:", err)
			return err
		}
		ok, err = enc.Verify(data)
		if !ok {
			fmt.Println("Verification failed after reconstruction, data likely corrupted.", err)
			return err
		} else {
			fmt.Println("Verification and Reconstructing data success")
		}
	}
	return nil
}
