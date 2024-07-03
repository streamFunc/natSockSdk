package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"github.com/klauspost/reedsolomon"
	"io"
	"os"
	"sort"
	"stun/hole"
	"sync"
	"time"
)

// flagOpType: ctrl ctrled http
var (
	flagNic          = flag.String("i", "", "which nic allowed")
	flagOpType       = flag.String("op", "", "operation type")
	flagSignalServer = flag.String("signal", "", "signal server address")
	flagRoom         = flag.String("room", "", "room name")
	flagPort         = flag.Int("port", 0, "listen port")
	flagStunServer   = flag.String("stun", "", "stun or turn server")
)

var fp *os.File
var receiveData [][]byte
var temp []byte
var tempSlice uint32
var notSend = true

//receiveData = make([][]byte, myDataShards+myParityShards)
//shards := make([][]byte, myDataShards+myParityShards)

var packetNo = 1
var isFirst = true
var countSlice = 0
var dc *UDPBuffer
var pc *PacketCache

// usage:
// -op ctrl -room roomName
// -op ctrled -room roomName
// http listenPort

type MsgDataCallback struct {
	// 这里可以添加其他需要的字段
}

// UDPData 结构表示UDP数据包
type UDPData struct {
	SequenceNumber int
	Payload        []byte
}

// UDPBuffer 结构表示UDP数据包缓存
type UDPBuffer struct {
	dataMap map[int][]byte
	mutex   sync.Mutex
}

// NewUDPBuffer 创建一个新的UDP数据包缓存
func NewUDPBuffer() *UDPBuffer {
	return &UDPBuffer{
		dataMap: make(map[int][]byte),
	}
}

// InsertData 将收到的UDP数据包按序插入缓存中
// 按排序后的序列号重建缓存 写文件
var count = 0

func (buf *UDPBuffer) InsertData(data UDPData, force bool) {
	buf.mutex.Lock()
	defer buf.mutex.Unlock()

	// 将数据包按序插入缓存中
	//buf.dataMap[data.SequenceNumber] = data.Payload
	buf.dataMap[data.SequenceNumber] = append(buf.dataMap[data.SequenceNumber], data.Payload...)

}

func (c *MsgDataCallback) OnDataReceived(data string, room string) {
	// 在这里处理回调收到的数据
	fmt.Println("OnDataReceived receive data:", data)
}

type PacketCache struct {
	cache map[int][][]byte
	mu    sync.Mutex
}

func NewPacketCache() *PacketCache {
	return &PacketCache{
		cache: make(map[int][][]byte),
	}
}

func (pc *PacketCache) AddPacketData(packetSeqNum, shardNum int, data []byte) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if _, ok := pc.cache[packetSeqNum]; !ok {
		pc.cache[packetSeqNum] = make([][]byte, hole.MyDataShards+hole.MyParityShards)
	}
	// Ensure the shardNum is valid and the data length is greater than zero
	if len(data) > 0 {
		pc.cache[packetSeqNum][shardNum] = append(pc.cache[packetSeqNum][shardNum], data...)
	}

	// 保持有序的键列表
	/*var sequenceNumbers []int
	for seq := range pc.cache {
		sequenceNumbers = append(sequenceNumbers, seq)
	}
	sort.Ints(sequenceNumbers)

	count++
	var sendNum int
	if len(sequenceNumbers)-10 > 0 {
		sendNum = len(sequenceNumbers) - 10
	} else {
		sendNum = len(sequenceNumbers)
	}
	// 进行fec解码
	if count > 10*(hole.MyDataShards+hole.MyParityShards+1) {
		count = 0
		enc, err := reedsolomon.New(hole.MyDataShards, hole.MyParityShards)
		if err != nil {
			return
		}

		for _, seq := range sequenceNumbers[:sendNum] {
			// Verify the shards
			ok, err := enc.Verify(pc.cache[seq])
			if ok {
				//fmt.Printf("No reconstruction needed, data is ok seq:%v\n", seq)
				for _, shard := range pc.cache[seq][:hole.MyDataShards] {
					fp.Write(shard)
				}
			} else {
				fmt.Println("Data lost. Reconstructing data:", seq)
				err = enc.Reconstruct(pc.cache[seq])
				if err != nil {
					fmt.Printf("Reconstruct failed seq:%v err:%v \n", seq, err)
					for n, shard := range pc.cache[seq][:hole.MyDataShards] {
						fmt.Printf("Is wrong data seq:%v slice:%v len:%v\n", seq, n, len(shard))
						fp.Write(shard)
					}
				} else {
					ok, err = enc.Verify(pc.cache[seq])
					if !ok {
						fmt.Println("Verification failed after reconstruction, data likely corrupted seq:", seq)
					} else {
						fmt.Println("Verification and Reconstructing data success seq:", seq)
						for _, shard := range pc.cache[seq][:hole.MyDataShards] {
							//fmt.Println("after Reconstructing len", len(shard))
							fp.Write(shard)
						}
					}
				}
			}
		}

		// 清除已写入的数据
		for _, key := range sequenceNumbers[:sendNum] {
			delete(pc.cache, key)
		}
	}*/
}

func (c *MsgDataCallback) OnDataReceivedByte(buf []byte, room string) {
	// 在这里处理回调收到的数据
	// Decode the data
	if hole.GlobalFecConfig {
		//g := hole.GetIceGateWay(room)

		// 从分片数据中提取包序号和分片号
		n := len(buf)
		packetSeqNum := binary.LittleEndian.Uint32(buf[:4])
		shardNum := binary.LittleEndian.Uint32(buf[4:8])
		data := buf[8:n]
		fmt.Printf("fec received packetSeqNum:%v shardNum:%v dataLen:%v \n", packetSeqNum, shardNum, len(data))

		pc.AddPacketData(int(packetSeqNum), int(shardNum), data)

	} else {
		packetSeqNum := binary.LittleEndian.Uint32(buf[:4])
		myData := buf[4:]
		fmt.Println("OnDataReceivedByte receive packetSeqNum: data len:", packetSeqNum, len(myData))
		//if fp != nil {
		//	fp.Write(myData)
		//}
		udpData := UDPData{
			SequenceNumber: int(packetSeqNum),
			Payload:        myData,
		}
		if dc != nil {
			dc.InsertData(udpData, false)
		}
	}

}

func xHandleFecDataTicker() {
	fmt.Println("test start...")
	ticker := time.Tick(time.Millisecond)

	// 读取文件内容并发送
	for range ticker {
		if hole.GlobalFecConfig {
			xHandleFecData()
		} else {
			xHandleData()
		}

	}

	fmt.Println("test end...")
}

func xHandleData() {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()

	// 保持有序的键列表
	var sequenceNumbers []int
	for seq := range dc.dataMap {
		sequenceNumbers = append(sequenceNumbers, seq)
	}
	sort.Ints(sequenceNumbers)

	if len(sequenceNumbers) < 1 {
		return
	}

	sendReady := false

	var sendNum int
	if len(sequenceNumbers)-5 > 0 {
		sendReady = true
		sendNum = len(sequenceNumbers) - 5
	} else {
		count++
		sendNum = len(sequenceNumbers)
	}

	if count > 10 {
		count = 0
		sendReady = true
		fmt.Printf("***************sendReady success,send buffer data sendNum:%v******************\n", sendNum)
	}

	// 输出有序数据
	if sendReady {
		for _, seq := range sequenceNumbers[:sendNum] {
			fmt.Printf("after sort seq:%v data len:%v\n", seq, len(dc.dataMap[seq]))
			payload := dc.dataMap[seq]
			if fp != nil {
				fp.Write(payload)
			}
		}

		// 清除已写入的数据
		for _, key := range sequenceNumbers[:sendNum] {
			delete(dc.dataMap, key)
		}
	}
}

func xHandleFecData() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	startTime := time.Now()
	// 保持有序的键列表
	var sequenceNumbers []int
	for seq := range pc.cache {
		sequenceNumbers = append(sequenceNumbers, seq)
	}
	sort.Ints(sequenceNumbers)

	if len(sequenceNumbers) < 1 {
		return
	}

	//lastSeq := sequenceNumbers[len(sequenceNumbers)-1]

	sendReady := false

	var sendNum int
	if len(sequenceNumbers)-5 > 0 {
		sendReady = true
		sendNum = len(sequenceNumbers) - 5
	} else {
		count++
		sendNum = len(sequenceNumbers)
	}

	if count > 6 {
		count = 0
		sendReady = true
		fmt.Printf("***************sendReady success,send buffer data sendNum:%v******************\n", sendNum)
	}

	if sendNum >= 2 {
		sendNum = sendNum - 1
	}
	// 进行fec解码
	if sendReady {
		enc, err := reedsolomon.New(hole.MyDataShards, hole.MyParityShards)
		if err != nil {
			return
		}

		for k, seq := range sequenceNumbers[:sendNum] {
			// Verify the shards
			ok, err := enc.Verify(pc.cache[seq])
			if ok {
				//fmt.Printf("No reconstruction needed, data is ok seq:%v\n", seq)
				for _, shard := range pc.cache[seq][:hole.MyDataShards] {
					fp.Write(shard)
				}
			} else {
				fmt.Println("Data lost. Reconstructing data:", seq)
				if sendNum == 1 { //只有一个包的时候再次等待
					return
				}
				if k == sendNum-1 { //最后一个数据包不完全，先不发送最后一个继续等待
					for _, key := range sequenceNumbers[:sendNum-1] {
						delete(pc.cache, key)
					}
					return
				}
				err = enc.Reconstruct(pc.cache[seq])
				if err != nil {
					fmt.Printf("Reconstruct failed seq:%v err:%v \n", seq, err)
					for n, shard := range pc.cache[seq][:hole.MyDataShards] {
						fmt.Printf("Is wrong data seq:%v slice:%v len:%v\n", seq, n, len(shard))
						fp.Write(shard)
					}
				} else {
					ok, err = enc.Verify(pc.cache[seq])
					if !ok {
						fmt.Println("Verification failed after reconstruction, data likely corrupted seq:", seq)
					} else {
						fmt.Println("Verification and Reconstructing data success seq:", seq)
						for _, shard := range pc.cache[seq][:hole.MyDataShards] {
							//fmt.Println("after Reconstructing len", len(shard))
							fp.Write(shard)
						}
					}
				}
			}
		}

		// 清除已写入的数据
		for _, key := range sequenceNumbers[:sendNum] {
			delete(pc.cache, key)
		}

		duration := time.Since(startTime)
		fmt.Printf("Execution time: %.2f microseconds\n", float64(duration.Microseconds()))
	}

}

func ReadMP4AndSendH264(room string, filePath string) {
	// 打开文件
	file, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer file.Close()

	// 创建一个缓冲区，用于读取文件内容
	buffer := make([]byte, 3600)

	ticker := time.Tick(40 * time.Millisecond)

	// 读取文件内容并发送
	for range ticker {
		//for {
		// 从文件中读取数据
		n, err := file.Read(buffer)
		if err != nil {
			if err != io.EOF {
				fmt.Println("Read file error:", err)
			}
			break
		}

		// 将读取到的数据发送到指定的接口
		hole.StartSendH264ByteQueue(room, buffer[:n])
	}
}

func main() {
	//fp, _ = os.Create("udp.264")
	//test()
	//return

	//fmt.Printf("hello test")
	//	hole.StartCtrl("", "3.95.67.53:9090", "turn://appcrash:testonly@3.95.67.53", "ctrl", "whatever")
	//return
	hole.SetFecEnable(false)
	//hole.SetFecParm(10, 8)
	flag.Parse()

	if len(*flagOpType) == 0 {
		panic("need more argument")
		return
	}
	fmt.Printf("flagNic:%v flagPort:%v flagOpType:%v flagSignalServer:%v flagRoom:%v flagStunServer:%v\n", *flagNic, *flagPort, *flagOpType, *flagSignalServer,
		*flagRoom, *flagStunServer)

	go xHandleFecDataTicker()

	//hole.StartTest(MyCallback)

	//if hole.GlobalFecConfig {
	//go handleData()
	//}

	/*go func() {
		// 检查并获取按序号排序的数据包
		retrievedData := dc.RetrieveData()
		for _, payload := range retrievedData {
			// 处理数据
			fmt.Printf("Received data from %s: %s\n", addr, string(payload))
		}
	}()*/

	call := &MsgDataCallback{}
	/*hole.RegisterIceCallback(func(data []byte) {
		fmt.Println("Received data:", string(data))
	})*/

	fp, _ = os.Create("myReceivedAndroid.264")
	dc = NewUDPBuffer()
	pc = NewPacketCache()

	hole.RegisterIceCallback(call)

	//var ctrlRole bool

	switch *flagOpType {
	case "http":
		fmt.Printf("hello http\n")
		hole.StartHttp(int64(*flagPort))
	case "ctrl":
		fmt.Printf("hello ctrl\n")
		//ctrlRole = true
		hole.StartConnect(*flagNic, *flagSignalServer, *flagStunServer, "ctrl", *flagRoom)
	case "ctrled":
		fmt.Printf("hello ctrled\n")
		hole.StartConnect(*flagNic, *flagSignalServer, *flagStunServer, "ctrled", *flagRoom)
	}
	fmt.Printf("hello test\n")
	time.Sleep(time.Second * 10)
	//hole.StartSendMsg("whatever", "ping")
	//data := []byte("Hello world")
	//hole.StartSendH264Byte("whatever", data)

	/*if hole.IsRoomConnected("whatever") {
		//data := []byte("Hello world")
		//hole.StartSendH264Byte("whatever", data)
		if ctrlRole {
			ReadMP4AndSendH264("whatever", "./raw.h264")
		} else {
			hole.StartReceivedData("whatever")
		}
	}*/

	//dc.writeCh <- struct{}{} // Trigger immediate write

	//stop := false
	for {
		// 阻塞一段时间
		time.Sleep(time.Second * 3)
		//if !stop {
		//hole.StopAllConnect()
		//stop = true
		//}

		//if hole.IsRoomConnected("whatever") {
		//	data := []byte("Hello world,my name is hahahah1234..")
		//	hole.StartSendH264ByteQueue("whatever", data)
		//}

	}
	if fp != nil {
		fp.Close()
		fp = nil
	}
}
