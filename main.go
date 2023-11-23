package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"stun/hole"
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

// usage:
// -op ctrl -room roomName
// -op ctrled -room roomName
// http listenPort

type MsgDataCallback struct {
	// 这里可以添加其他需要的字段
}

func (c *MsgDataCallback) OnDataReceived(data string) {
	// 在这里处理回调收到的数据
	fmt.Println("OnDataReceived receive data:", data)
}

func (c *MsgDataCallback) OnDataReceivedByte(data []byte) {
	// 在这里处理回调收到的数据
	fmt.Println("OnDataReceivedByte receive data:")
	if fp != nil {
		fp.Write(data)
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
	buffer := make([]byte, 1024)

	// 创建一个定时通道，每隔40毫秒发送一个时间
	ticker := time.Tick(40 * time.Millisecond)

	// 读取文件内容并发送
	for range ticker {
		// 从文件中读取数据
		n, err := file.Read(buffer)
		if err != nil {
			if err != io.EOF {
				fmt.Println("Read file error:", err)
			}
			break
		}

		// 将读取到的数据发送到指定的接口
		hole.StartSendH264Byte(room, buffer[:n])
	}
}

func main() {

	//fmt.Printf("hello test")
	//	hole.StartCtrl("", "3.95.67.53:9090", "turn://appcrash:testonly@3.95.67.53", "ctrl", "whatever")
	//return
	flag.Parse()

	if len(*flagOpType) == 0 {
		panic("need more argument")
		return
	}
	fmt.Printf("flagNic:%v flagPort:%v flagOpType:%v flagSignalServer:%v flagRoom:%v flagStunServer:%v\n", *flagNic, *flagPort, *flagOpType, *flagSignalServer,
		*flagRoom, *flagStunServer)

	//hole.StartTest(MyCallback)

	call := &MsgDataCallback{}
	/*hole.RegisterIceCallback(func(data []byte) {
		fmt.Println("Received data:", string(data))
	})*/

	fp, _ = os.Create("myReceivedAndroid.264")
	hole.RegisterIceCallback(call)

	switch *flagOpType {
	case "http":
		fmt.Printf("hello http\n")
		hole.StartHttp(int64(*flagPort))
	case "ctrl":
		fmt.Printf("hello ctrl\n")
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

	if hole.IsRoomConnected("whatever") {
		//ReadMP4AndSendH264("whatever", "./raw.h264")
		data := []byte("Hello world")
		hole.StartSendH264Byte("whatever", data)
	}

	//stop := false
	for {
		// 阻塞一段时间
		time.Sleep(time.Second)
		//if !stop {
		//hole.StopAllConnect()
		//stop = true
		//}

	}
	if fp != nil {
		fp.Close()
		fp = nil
	}
}
