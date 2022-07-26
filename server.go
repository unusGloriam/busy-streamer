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
	PORT                  = "80" //используем, согласно стандарту, порт 80 для нешифрованной передачи данных по веб-сокету
	SERV_ADDR             = "localhost:" + PORT
	STREAM_URL            = "rtsp://rtsp.stream/pattern"
	DIAL_TIMEOUT_DURATION = 3 * time.Second               //время на "дозвон" до медиа потока
	READ_TIMEOUT_DURATION = 3 * time.Second               //время на осуществление операций чтения или записи
	PING_DURATION         = 15 * time.Second              //каждые 15 секунд проверяем наличие видео сигнала
	PING_DURATION_RESTART = PING_DURATION + 1*time.Second //если в течение 15-16 секунд есть видео-кадр - ещё 16 секунд не беспокоим
)

var upgrader = websocket.Upgrader{ //пусть при апгрейде соединения до веб-сокетного источник подключения всегда "хороший"
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func SocketHandler(w http.ResponseWriter, r *http.Request) { //действия сервера при подключении клиента к веб-сокету
	connection, error_code := upgrader.Upgrade(w, r, nil) //апгрейд соединения до веб-сокетного
	if error_code != nil {
		log.Println("[server_socket]Failed to upgrade connection: " + error_code.Error())
		connection.Close()
	}
	log.Println(w, "Connection accepted, dialing media stream...")   //в консоль выводим сообщение об успешном соединении с клиентом
	new_session, error_code := rtspv2.Dial(rtspv2.RTSPClientOptions{ //подключение сервера к RTSP трансляции
		URL:              STREAM_URL,
		DisableAudio:     true,
		DialTimeout:      DIAL_TIMEOUT_DURATION,
		ReadWriteTimeout: READ_TIMEOUT_DURATION,
		Debug:            false,
	})
	if error_code != nil {
		log.Println("[server_socket]Failed to connect to the stream: " + error_code.Error())
	}

	ping_stream := time.NewTimer(PING_DURATION) //заведём таймер, чтобы каждые 15-16 секунд проверять наличие видео
	var timeLine = make(map[int8]time.Duration) //сделаем возможность перемотки буферизованной части трансляции

	muxer := mp4f.NewMuxer(nil)                           //создали новый мультиплексор для кадров видео формата mp4
	error_code = muxer.WriteHeader(new_session.CodecData) //при передаче данных через мультиплексор в заголовке данных содержится информация о типе кодека
	if error_code != nil {
		log.Println("muxer.WriteHeader", error_code)
		return
	}
	meta, init := muxer.GetInit(new_session.CodecData)                                        //получаем кадр инициализации через данные о кодеке
	error_code = connection.WriteMessage(websocket.BinaryMessage, append([]byte{9}, meta...)) //передаём метаданные
	if error_code != nil {
		log.Println("websocket.Message.Send", error_code)
		return
	}
	error_code = connection.WriteMessage(websocket.BinaryMessage, init) //передаём кадр инициализации
	if error_code != nil {
		return
	}
	for { //бесконечно передаём данные на клиентскую часть
		select {
		case <-ping_stream.C: //если за 15-16 секунд будильник так и не сбросился - нету видео
			log.Println("[stream]Error: the stream has no video")
			return
		case packet_buffer := <-new_session.OutgoingPacketQueue: //при получении данных от трансляции
			if packet_buffer.IsKeyFrame { //получили очередной видео кадр
				ping_stream.Reset(PING_DURATION_RESTART) //сбрасываем будильник
			}

			log.Println(w, "Transmitting data packet...")
			timeLine[packet_buffer.Idx] += packet_buffer.Duration //поддерживаем в тонусе полоску времени
			packet_buffer.Time = timeLine[packet_buffer.Idx]
			ready, buf, _ := muxer.WritePacket(*packet_buffer, false) //передаём полученные данные на мультиплексор
			if ready {                                                //после передачи
				error_code = connection.SetWriteDeadline(time.Now().Add(10 * time.Second)) //даём 10 секунд на передачу пакета данных клиенту
				if error_code != nil {
					return
				}
				error_code = connection.WriteMessage(websocket.BinaryMessage, buf) //передаём данные клиенту
				if error_code != nil {
					return
				}
			}
		}
	}
}
func main() {
	router := gin.Default() //создаём стандартные gin-роутер

	router.LoadHTMLFiles("index.html")     //подгружаем файл клиентской части
	router.GET("/", func(c *gin.Context) { //пропишем обработчик запроса GET по пути "/"
		c.HTML(200, "index.html", nil) //при этом запросе обращаемся к скрипту html-файла
	})
	router.GET("/load", func(c *gin.Context) { //пропишем обработчик инициации подключения по веб-сокету от html-скрипта (GET по пути "/load")
		SocketHandler(c.Writer, c.Request) //переходим к обработчику взаимодействия по сокету
	})
	router.Run(SERV_ADDR) //поднимаем сервер
}
