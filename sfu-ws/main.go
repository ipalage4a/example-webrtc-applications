package main

import (
	"context"
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"sync"
	"text/template"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

var (
	addr     = flag.String("addr", ":8080", "http service address")
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	indexTemplate = &template.Template{}

	// lock for peerConnections and trackLocals
	listLock        sync.RWMutex
	peerConnections = make(map[int]*peerConnectionState)
	trackLocals     map[string]*webrtc.TrackLocalStaticRTP
)

type websocketMessage struct {
	Event string `json:"event"`
	Data  string `json:"data"`
	Id    int    `json:"id"`
}

type peerConnectionState struct {
	*webrtc.PeerConnection
	websocket *threadSafeWriter
	mux       sync.Mutex
	mayOffer  func()
}

func main() {
	// Parse the flags passed to program
	flag.Parse()

	// Init other state
	log.SetFlags(0)
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{}

	// Read index.html from disk into memory, serve whenever anyone requests /
	indexHTML, err := ioutil.ReadFile("index.html")
	if err != nil {
		panic(err)
	}
	indexTemplate = template.Must(template.New("").Parse(string(indexHTML)))

	// websocket handler
	http.HandleFunc("/websocket", websocketHandler)

	// index.html handler
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if err := indexTemplate.Execute(w, "ws://"+r.Host+"/websocket"); err != nil {
			log.Fatal(err)
		}
	})

	// request a keyframe every 3 seconds
	go func() {
		for range time.NewTicker(time.Second * 3).C {
			dispatchKeyFrame()
		}
	}()

	// start HTTP server
	log.Fatal(http.ListenAndServe(*addr, nil))
}

// Add to list of tracks and fire renegotation for all PeerConnections
func addTrack(t *webrtc.TrackRemote) *webrtc.TrackLocalStaticRTP {
	listLock.Lock()
	defer func() {
		listLock.Unlock()
		signalPeerConnections("add track")
	}()

	// Create a new TrackLocal with the same codec as our incoming
	trackLocal, err := webrtc.NewTrackLocalStaticRTP(t.Codec().RTPCodecCapability, t.ID(), t.StreamID())
	if err != nil {
		panic(err)
	}

	trackLocals[t.ID()] = trackLocal
	return trackLocal
}

// Remove from list of tracks and fire renegotation for all PeerConnections
func removeTrack(t *webrtc.TrackLocalStaticRTP) {
	listLock.Lock()
	defer func() {
		listLock.Unlock()
		signalPeerConnections("remove track")
	}()

	delete(trackLocals, t.ID())
}

// signalPeerConnections updates each PeerConnection so that it is getting all the expected media tracks
func signalPeerConnections(reason string) {
	log.Println("signal reason: ", reason)
	attemptSync := func() (tryAgain bool) {
		listLock.Lock()
		defer func() {
			listLock.Unlock()
			dispatchKeyFrame()
		}()

		for i, pc := range peerConnections {
			log.Println("signal for ", i, " peer")
			switch pc.ConnectionState() {
			case webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateDisconnected:
				delete(peerConnections, i)
				log.Println("remove peer")
				return true
			}

			// map of sender we already are seanding, so we don't double send
			existingSenders := map[string]bool{}

			for _, sender := range pc.GetSenders() {
				if sender.Track() == nil {
					continue
				}

				existingSenders[sender.Track().ID()] = true

				// If we have a RTPSender that doesn't map to a existing track remove and signal
				if _, ok := trackLocals[sender.Track().ID()]; !ok {
					if err := pc.RemoveTrack(sender); err != nil {
						return true
					}
				}
			}

			// Don't receive videos we are sending, make sure we don't have loopback
			for _, receiver := range pc.GetReceivers() {
				if receiver.Track() == nil {
					continue
				}

				existingSenders[receiver.Track().ID()] = true
			}

			// Add all track we aren't sending yet to the PeerConnection
			for trackID := range trackLocals {
				if _, ok := existingSenders[trackID]; !ok {
					if _, err := pc.AddTrack(trackLocals[trackID]); err != nil {
						return true
					}
				}
			}

			offer, err := pc.CreateOffer(nil)
			if err != nil {
				log.Println(err)
				return true
			}

			// Этот контекст используется для проверки надлежащего состояния для установки нового оффера в локал дескрипшен
			// Если сейчас состояние стейбл до завершаем контекст
			// Иначе ждем пока контекст завершит каллбек на изменение сигналинг стейта
			var ctx context.Context
			ctx, pc.mayOffer = context.WithCancel(context.Background())
			if pc.SignalingState() == webrtc.SignalingStateStable {
				log.Println("SS: signaling is stable")
				pc.mayOffer()
			} else {
				log.Println("SS: waiting stable")
			}
			<-ctx.Done()

			if err = pc.SetLocalDescription(offer); err != nil {

				// var ctx context.Context
				// ctx, mayOffer = context.WithCancel(context.Background())
				// if pc.SignalingState() == webrtc.SignalingStateStable {
				// 	log.Println("SS: signaling is stable")
				// 	mayOffer()
				// } else {
				// 	log.Println("SS: waiting stable")
				// }
				// <-ctx.Done()

				if err = pc.SetLocalDescription(offer); err != nil {
					log.Println("second: ", err)
					return true
				}
				log.Println("now is okay")
			}

			offerString, err := json.Marshal(offer)
			if err != nil {
				return true
			}

			if err = pc.websocket.WriteJSON(&websocketMessage{
				Event: "sdp",
				Data:  string(offerString),
			}); err != nil {
				return true
			}
		}

		return
	}

	for syncAttempt := 0; ; syncAttempt++ {
		if syncAttempt == 25 {
			log.Println("max attempts reached: ", syncAttempt)
			return
		}

		if !attemptSync() {
			break
		}
	}
}

// dispatchKeyFrame sends a keyframe to all PeerConnections, used everytime a new user joins the call
func dispatchKeyFrame() {
	listLock.Lock()
	defer listLock.Unlock()

	for i := range peerConnections {
		for _, receiver := range peerConnections[i].GetReceivers() {
			if receiver.Track() == nil {
				continue
			}

			_ = peerConnections[i].WriteRTCP([]rtcp.Packet{
				&rtcp.PictureLossIndication{
					MediaSSRC: uint32(receiver.Track().SSRC()),
				},
			})
		}
	}
}

// Обработчик входящих сообщений
func websocketHandler(w http.ResponseWriter, r *http.Request) {
	// Upgrade HTTP request to Websocket
	unsafeConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}

	c := &threadSafeWriter{unsafeConn, sync.Mutex{}}

	// When this frame returns close the Websocket
	defer c.Close() //nolint

	message := &websocketMessage{}

	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			log.Println(err)
			return
		} else if err := json.Unmarshal(raw, &message); err != nil {
			log.Println(err)
			return
		}

		log.Println("received: ", message.Event)
		switch message.Event {
		case "candidate":
			candidate := webrtc.ICECandidateInit{}
			if err := json.Unmarshal([]byte(message.Data), &candidate); err != nil {
				log.Println(err)
				return
			}

			if err := peerConnections[message.Id].AddICECandidate(candidate); err != nil {
				log.Println(err)
				return
			}
		// Предварительная установка вебртс соединения
		case "offer":
			var err error
			peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{})
			if err != nil {
				log.Print(err)
				return
			}
			// When this frame returns close the PeerConnection
			defer peerConnection.Close() //nolint

			// Accept one audio and one video track incoming
			for _, typ := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
				if _, err := peerConnection.AddTransceiverFromKind(typ, webrtc.RTPTransceiverInit{
					Direction: webrtc.RTPTransceiverDirectionRecvonly,
				}); err != nil {
					log.Print(err)
					return
				}
			}

			pc := &peerConnectionState{peerConnection, c, sync.Mutex{}, nil}
			// Add our new PeerConnection to global list
			listLock.Lock()
			peerConnections[message.Id] = pc
			listLock.Unlock()

			// If PeerConnection is closed remove it from global list
			peerConnections[message.Id].OnConnectionStateChange(func(p webrtc.PeerConnectionState) {
				switch p {
				case webrtc.PeerConnectionStateFailed:
					if err := peerConnections[message.Id].Close(); err != nil {
						log.Print(err)
					}
				case webrtc.PeerConnectionStateClosed:
					signalPeerConnections("peer closed")
				}
			})

			peerConnections[message.Id].OnSignalingStateChange(func(ss webrtc.SignalingState) {
				switch ss {
				case webrtc.SignalingStateStable:
					log.Println("SS: may offer!")
					if pc.mayOffer != nil {
						log.Println("SS: call done may offer")
						pc.mayOffer()
					}
				default:
				}
			})

			peerConnections[message.Id].OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
				// Create a track to fan out our incoming video to all peers
				trackLocal := addTrack(t)
				defer removeTrack(trackLocal)

				buf := make([]byte, 1500)
				for {
					i, _, err := t.Read(buf)
					if err != nil {
						return
					}

					if _, err = trackLocal.Write(buf[:i]); err != nil {
						return
					}
				}
			})

			//Устанавливаем оффер с клиент и отправляем обратно ансвер
			description := webrtc.SessionDescription{}
			if err := json.Unmarshal([]byte(message.Data), &description); err != nil {
				log.Println(err)
				return
			}

			if err := peerConnections[message.Id].SetRemoteDescription(description); err != nil {
				log.Println(err)
				return
			}

			answer, err := peerConnections[message.Id].CreateAnswer(nil)
			if err != nil {
				log.Println(err)
				return
			}

			peerConnections[message.Id].mux.Lock()
			if err = peerConnections[message.Id].SetLocalDescription(answer); err != nil {
				peerConnections[message.Id].mux.Unlock()
				log.Println(err)
				return
			}
			peerConnections[message.Id].mux.Unlock()

			// Отправляем локал дескрипшен, потому что в ансвере нет айс-кандидатов
			answerString, err := json.Marshal(peerConnections[message.Id].LocalDescription())
			if err != nil {
				return
			}

			if err = c.WriteJSON(&websocketMessage{
				Event: "sdp",
				Data:  string(answerString),
			}); err != nil {
				return
			}

			// Signal for the new PeerConnection
			signalPeerConnections("init")

		case "sdp":
			// Получение и установка RemoteDescription
			// Так же если description.Type == "offer" отправляем ответ обратно
			description := webrtc.SessionDescription{}
			if err := json.Unmarshal([]byte(message.Data), &description); err != nil {
				log.Println(err)
				return
			}

			log.Println("\tsdp type: ", description.Type)

			if err := peerConnections[message.Id].SetRemoteDescription(description); err != nil {
				log.Println(err)
				return
			}

			if description.Type == webrtc.SDPTypeOffer {
				answer, err := peerConnections[message.Id].CreateAnswer(nil)
				if err != nil {
					log.Println(err)
					return
				}

				peerConnections[message.Id].mux.Lock()
				if err = peerConnections[message.Id].SetLocalDescription(answer); err != nil {
					peerConnections[message.Id].mux.Unlock()
					log.Println(err)
					return
				}
				peerConnections[message.Id].mux.Unlock()

				answerString, err := json.Marshal(answer)
				if err != nil {
					return
				}

				if err = c.WriteJSON(&websocketMessage{
					Event: "sdp",
					Data:  string(answerString),
				}); err != nil {
					return
				}
			}
		}
	}
}

// Helper to make Gorilla Websockets threadsafe
type threadSafeWriter struct {
	*websocket.Conn
	sync.Mutex
}

func (t *threadSafeWriter) WriteJSON(v interface{}) error {
	t.Lock()
	defer t.Unlock()

	return t.Conn.WriteJSON(v)
}
