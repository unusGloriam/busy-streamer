package main

import (
	"log"
	"net/http"
	"time"

	"github.com/deepch/vdk/format/mp4f"
	"github.com/deepch/vdk/format/rtspv2"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	PORT                  = "80"
	SERV_ADDR             = "localhost:" + PORT
	STREAM_URL            = "rtsp://rtsp.stream/pattern"
	DIAL_TIMEOUT_DURATION = 3 * time.Second
	READ_TIMEOUT_DURATION = 3 * time.Second
	PING_DURATION         = 15 * time.Second
	PING_DURATION_RESTART = PING_DURATION + 1*time.Second
	IS_AUDIO_ONLY         = false
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func SocketHandler(w http.ResponseWriter, r *http.Request) { //Handler of a client's connection to the server
	connection, error_code := upgrader.Upgrade(w, r, nil)
	if error_code != nil {
		log.Println("[server_socket]Failed to upgrade connection: " + error_code.Error())
		connection.Close()
	}
	//TODO: Streamsource selection
	log.Println(w, "Connection accepted, dialing media stream...")
	new_session, error_code := rtspv2.Dial(rtspv2.RTSPClientOptions{ //makin' a client
		URL:              STREAM_URL,
		DisableAudio:     true,
		DialTimeout:      DIAL_TIMEOUT_DURATION,
		ReadWriteTimeout: READ_TIMEOUT_DURATION,
		Debug:            false,
	})
	if error_code != nil {
		log.Println("[server_socket]Failed to connect to the stream: " + error_code.Error())
	}

	ping_stream := time.NewTimer(PING_DURATION)
	var timeLine = make(map[int8]time.Duration)

	muxer := mp4f.NewMuxer(nil)
	error_code = muxer.WriteHeader(new_session.CodecData)
	if error_code != nil {
		log.Println("muxer.WriteHeader", error_code)
		return
	}
	meta, init := muxer.GetInit(new_session.CodecData)
	error_code = connection.WriteMessage(websocket.BinaryMessage, append([]byte{9}, meta...))
	if error_code != nil {
		log.Println("websocket.Message.Send", error_code)
		return
	}
	connection.WriteMessage(websocket.BinaryMessage, init)
	if error_code != nil {
		return
	}
	for {
		select {
		case <-ping_stream.C:
			log.Println("[stream]Error: the stream has no video")
			return
		case packet_buffer := <-new_session.OutgoingPacketQueue:
			if packet_buffer.IsKeyFrame || IS_AUDIO_ONLY == true {
				ping_stream.Reset(PING_DURATION_RESTART)
			}

			log.Println(w, "Transmitting data packet...")
			timeLine[packet_buffer.Idx] += packet_buffer.Duration
			packet_buffer.Time = timeLine[packet_buffer.Idx]
			ready, buf, _ := muxer.WritePacket(*packet_buffer, false)
			if ready {
				error_code = connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if error_code != nil {
					return
				}
				err := connection.WriteMessage(websocket.BinaryMessage, buf)
				if err != nil {
					return
				}
			}
		}
	}
}
func main() {
	router := gin.Default()

	router.LoadHTMLFiles("index.html")
	router.GET("/", func(c *gin.Context) {
		c.HTML(200, "index.html", nil)
	})
	router.GET("/load", func(c *gin.Context) {
		SocketHandler(c.Writer, c.Request)
	})
	router.Run(SERV_ADDR)
}
