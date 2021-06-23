package main

import (
	"C"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/pion/webrtc/v3"

	"github.com/ipalage4a/go-glib/glib"
	pionSignal "github.com/pion/example-webrtc-applications/v3/internal/signal"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/tinyzimmer/go-gst/gst/app"

	"github.com/pion/rtcp"
	"github.com/tinyzimmer/go-gst/gst"
)

// RunLoop is used to wrap the given function in a main loop and print any error.
// The main loop itself is passed to the function for more control over exiting.
func RunLoop(f func(*glib.MainLoop) error) {
	mainLoop := glib.NewMainLoop(glib.MainContextDefault(), false)

	if err := f(mainLoop); err != nil {
		fmt.Println("ERROR!", err)
	}
}

func mainLoop(loop *glib.MainLoop, pipeline *gst.Pipeline) error {
	// Start the pipeline

	// Due to recent changes in the bindings - the finalizers might fire on the pipeline
	// prematurely when it's passed between scopes. So when you do this, it is safer to
	// take a reference that you dispose of when you are done. There is an alternative
	// to this method in other examples.
	pipeline.Ref()
	defer pipeline.Unref()

	pipeline.SetState(gst.StatePlaying)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		pipeline.SendEvent(gst.NewEOSEvent())
	}()

	// Retrieve the bus from the pipeline and add a watch function
	pipeline.GetPipelineBus().AddWatch(func(msg *gst.Message) bool {
		if err := handleMessage(msg); err != nil {
			loop.Quit()
			panic(err)
			// return false
		}
		return true
	})

	loop.Run()

	return nil
}

type peer struct {
	peer        *webrtc.PeerConnection
	track       *webrtc.TrackLocalStaticSample
	remoteTrack *webrtc.TrackRemote
	src         *app.Source
	sink        *app.Sink
	elems       []*gst.Element
	ready       chan bool

	debug struct {
		push interface{}
		pull interface{}
	}
}

func (p *peer) sync() {
	for _, el := range p.elems {
		el.SyncStateWithParent()
	}
}

func (p *peer) logState() {
	fmt.Printf("\n%s: %s\n", pipeline.TypeFromInstance().Name(), pipeline.GetState())
	for _, el := range p.elems {
		fmt.Printf("%s: %s\n", el.TypeFromInstance().Name(), el.GetState())
	}
	fmt.Print("\n")
}

func (p *peer) tee() *gst.Element {
	return p.elems[4]
}

func (p *peer) mixer() *gst.Element {
	return p.elems[5]
}

func (p *peer) teeToPeer(other *peer) {

	var src *gst.Pad
	var sink *gst.Pad

	for _, v := range p.tee().GetPadTemplates() {
		if v.Name() != "src_%u" {
			continue
		}
		existPads, _ := p.tee().GetSrcPads()
		src = gst.NewPadFromTemplate(v, "src_"+strconv.Itoa(len(existPads)))
		if ok := p.tee().AddPad(src); ok {
			break
		}

	}

	for _, v := range other.mixer().GetPadTemplates() {
		if v.Name() != "sink_%u" {
			continue
		}
		existPads, _ := other.mixer().GetSinkPads()
		sink = gst.NewPadFromTemplate(v, "sink_"+strconv.Itoa(len(existPads)))
		if ok := other.mixer().AddPad(sink); ok {
			break
		}
	}

	src.Link(sink)

	// pipeline.SetState(gst.StatePlaying)

	// p.logState()
}

var listLock sync.Mutex
var peers map[int]*peer = make(map[int]*peer)
var pipeline *gst.Pipeline

var config webrtc.Configuration

func getPeers() map[int]*peer {
	listLock.Lock()
	defer listLock.Unlock()
	return peers
}

func addPeer(key int, p *peer) {
	listLock.Lock()
	defer listLock.Unlock()
	peers[key] = p
}

func initPipeline() (err error) {
	pipeline, err = gst.NewPipeline("pipeline-test")
	if err != nil {
		panic(err)
	}
	pipeline.SetState(gst.StatePlaying)
	return
}

func handleMessage(msg *gst.Message) error {
	switch msg.Type() {
	case gst.MessageEOS:
		return app.ErrEOS
	case gst.MessageError:
		gerr := msg.ParseError()
		if debug := gerr.DebugString(); debug != "" {
			fmt.Println(debug)
		}
		return gerr
	default:
		fmt.Println(msg)
	}
	return nil
}

func initPeer(i int) (p *peer, err error) {
	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}

	// Create a audio track
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: "audio/opus"}, "audio", "pion1")
	if err != nil {
		panic(err)
	}

	rtpSender, err := peerConnection.AddTrack(track)
	if err != nil {
		panic(err)
	}

	go func() {
		rtcpBuf := make([]byte, 1500)
		for {
			if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
				return
			}
		}
	}()

	elems, err := gst.NewElementMany("appsrc", "queue2", "rtpopusdepay", "opusdec", "tee", "adder", "audioconvert", "queue2", "opusenc", "appsink")
	if err != nil {
		panic(err)
	}

	src := app.SrcFromElement(elems[0])

	propType, err := src.GetPropertyType("format")
	panic(propType.Name())

	// formatTime := glib.TypeFromName("GstFormat")
	err = src.SetProperty("format", 3)
	if err != nil {
		panic(err)
	}
	prop, err := src.GetProperty("format")
	panic(prop)

	// src.Element.SetProperty("format", int(gst.FormatTime))
	// err = src.SetFormat(gst.FormatTime)

	src.SetProperty("is-live", true)
	src.SetProperty("do-timestamp", true)
	src.SetProperty("max-bytes", int64(0))

	xrtpCaps := gst.NewCapsFromString(
		// "application/x-rtp, payload=96, encoding-name=OPUS",
		"application/x-rtp, payload=(int)96, encoding-name=(string)OPUS, media=(string)audio, clock-rate=(int)48000",
	)
	src.SetCaps(xrtpCaps)

	sink := app.SinkFromElement(elems[9])

	p = &peer{
		peer:  peerConnection,
		track: track,
		src:   src,
		sink:  sink,
		elems: elems,
		ready: make(chan bool, 1),
	}

	src.SetCallbacks(&app.SourceCallbacks{
		NeedDataFunc: func(self *app.Source, _ uint) {
			buf := make([]byte, 1500)
			var i int

			<-p.ready

			// for {
			i, _, err = p.remoteTrack.Read(buf)
			if err != nil {
				panic(err)
			}

			buffer := gst.NewBufferFromBytes(buf[:i])

			// Push the buffer onto the pipeline.
			p.src.PushBuffer(buffer)
			p.debug.push = strconv.Itoa(i) + " " + time.Now().String()
			// }
		},
	})

	// Getting data out of the appsink is done by setting callbacks on it.
	// The appsink will then call those handlers, as soon as data is available.
	sink.SetCallbacks(&app.SinkCallbacks{
		// Add a "new-sample" callback
		NewSampleFunc: func(sink *app.Sink) gst.FlowReturn {
			sample := p.sink.PullSample()
			if sample == nil {
				return gst.FlowEOS
			}

			// Retrieve the buffer from the sample
			buffer := sample.GetBuffer()
			if buffer == nil {
				return gst.FlowEOS
			}

			now := time.Now()
			tps := now.Truncate(buffer.PresentationTimestamp())

			s := media.Sample{Data: buffer.Bytes(), Duration: buffer.Duration(), Timestamp: tps}
			p.debug.pull = time.Now()

			if err := p.track.WriteSample(s); err != nil {
				panic(err)
			}
			return gst.FlowOK
		},
	})

	pipeline.AddMany(elems...)

	elems[0].Link(elems[2])
	// elems[1].Link(elems[2])
	elems[2].Link(elems[9])
	// elems[3].Link(elems[9])

	// p.teeToPeer(p)

	// elems[5].Link(elems[6])
	// elems[6].Link(elems[7])
	// elems[7].Link(elems[8])
	// elems[8].Link(elems[9])

	pipeline.SetState(gst.StatePlaying)
	addPeer(i, p)

	// go func() {
	// 	for {
	// 		select {
	// 		case <-p.ready:
	// 			// pipeline.SetState(gst.StatePlaying)
	// 			// for key, peer := range getPeers() {
	// 			// fmt.Printf("\n\nconnect %d to %d peer\n\n", i, key)
	// 			p.teeToPeer(p)
	// 			// fmt.Printf("\n\nconnect %d to %d peer\n\n", key, i)
	// 			// peer.teeToPeer(p)
	// 			// }
	// 			addPeer(i, p)
	// 			pipeline.DebugBinToDotFile(gst.DebugGraphShowAll, strconv.Itoa(i)+"debug")

	// 			p.logState()
	// 		default:
	// 		}
	// 	}
	// }()

	pipeline.DebugBinToDotFile(gst.DebugGraphShowAll, strconv.Itoa(i)+"debug")
	// p.logState()

	return
}

// gstreamerReceiveMain is launched in a goroutine because the main thread is needed
// for Glib's main loop (Gstreamer uses Glib)
func gstreamerReceiveMain() {
	// Everything below is the pion-WebRTC API! Thanks for using it ❤️.

	err := initPipeline()
	defer pipeline.SetState(gst.StateNull)

	if err != nil {
		panic(err)
	}

	config = webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	localSdp := make(chan string, 1)
	sdpChan := pionSignal.HTTPSDPServerWithAnswer(localSdp)

	var peerKey int
	for {

		// Wait for the offer to be pasted
		offer := webrtc.SessionDescription{}

		// pionSignal.Decode(pionSignal.MustReadStdin(), &offer)
		pionSignal.Decode(<-sdpChan, &offer)

		peerKey++

		p, err := initPeer(peerKey)
		if err != nil {
			panic(err)
		}

		p.peer.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
			fmt.Printf("OnTrack fired on %d peer\n", peerKey)

			p.remoteTrack = track
			p.ready <- true

			// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
			go func() {
				ticker := time.NewTicker(time.Second * 1)
				for range ticker.C {
					rtcpSendErr := p.peer.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}})
					if rtcpSendErr != nil {
						fmt.Println(rtcpSendErr)
					}
				}
			}()

			// buf := make([]byte, 1500)
			// var i int
			// for {
			// 	i, _, err = track.Read(buf)
			// 	if err != nil {
			// 		panic(err)
			// 	}

			// 	// if err := p.src.PushBuffer(buf[:i]); err != nil {
			// 	// 	panic(err)
			// 	// }

			// 	// Create a buffer that can hold exactly one video RGBA frame.
			// 	buffer := gst.NewBufferWithSize(int64(i))

			// 	// For each frame we produce, we set the timestamp when it should be displayed
			// 	// The autovideosink will use this information to display the frame at the right time.
			// 	// buffer.SetPresentationTimestamp(time.Duration(i) + 100*time.Millisecond)

			// 	// // Produce an image frame for this iteration.
			// 	// pixels := produceImageFrame(palette[i])

			// 	// At this point, buffer is only a reference to an existing memory region somewhere.
			// 	// When we want to access its content, we have to map it while requesting the required
			// 	// mode of access (read, read/write).
			// 	// See: https://gstreamer.freedesktop.org/documentation/plugin-development/advanced/allocation.html
			// 	//
			// 	// There are convenience wrappers for building buffers directly from byte sequences as
			// 	// well.
			// 	buffer.Map(gst.MapWrite).WriteData(buf[:i])
			// 	buffer.Unmap()

			// 	// Push the buffer onto the pipeline.
			// 	p.debug.push = strconv.Itoa(i) + " " + time.Now().String()
			// 	p.src.PushBuffer(buffer)
			// }

		})

		// Set the handler for ICE connection state
		// This will notify you when the peer has connected/disconnected
		p.peer.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
			fmt.Printf("Connection State has changed %s \n", connectionState.String())
		})

		// Set the remote SessionDescription
		err = p.peer.SetRemoteDescription(offer)
		if err != nil {
			panic(err)
		}

		// Create an answer
		answer, err := p.peer.CreateAnswer(nil)
		if err != nil {
			panic(err)
		}

		// Create channel that is blocked until ICE Gathering is complete
		gatherComplete := webrtc.GatheringCompletePromise(p.peer)

		// Sets the LocalDescription, and starts our UDP listeners
		err = p.peer.SetLocalDescription(answer)
		if err != nil {
			panic(err)
		}

		// Block until ICE Gathering is complete, disabling trickle ICE
		// we do this because we only can exchange one signaling message
		// in a production application you should exchange ICE Candidates via OnICECandidate
		<-gatherComplete

		// Output the answer in base64 so we can paste it in browser
		fmt.Println(pionSignal.Encode(*p.peer.LocalDescription()))
		localSdp <- pionSignal.Encode(*p.peer.LocalDescription())
	}
}

func main() {
	// Start a new thread to do the actual work for this application
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	go func() {
		for range ticker.C {
			ps := getPeers()
			var keys []int
			for k := range ps {
				keys = append(keys, k)
			}
			sort.Ints(keys)

			if len(ps) != 0 {
				fmt.Printf("\n--------------")
				for _, k := range keys {
					fmt.Printf("\ndebug peer %d\n", k)
					fmt.Printf("\tpush: %v\n", ps[k].debug.push)
					fmt.Printf("\tpull: %v\n", ps[k].debug.pull)
				}
				fmt.Printf("--------------\n")
			}
		}
	}()

	// go func() {
	// 	for {
	// 		// ts := time.Now().Add(time.Millisecond * 500)
	// 		for _, p := range getPeers() {
	// 			sample := p.sink.PullSample()
	// 			if sample == nil {
	// 				continue
	// 			}

	// 			// Retrieve the buffer from the sample
	// 			buffer := sample.GetBuffer()
	// 			if buffer == nil {
	// 				continue
	// 			}

	// 			now := time.Now()
	// 			tps := now.Truncate(buffer.PresentationTimestamp())

	// 			s := media.Sample{Data: buffer.Bytes(), Duration: buffer.Duration(), Timestamp: tps}
	// 			p.debug.pull = s

	// 			if err := p.track.WriteSample(s); err != nil {
	// 				panic(err)
	// 			}
	// 		}
	// 	}
	// }()

	gst.Init(nil)

	// Create a main loop. This is only required when utilizing signals via the bindings.
	// In this example, the AddWatch on the pipeline bus requires iterating on the main loop.

	go gstreamerReceiveMain()

	RunLoop(func(loop *glib.MainLoop) error {
		for {
			if pipeline != nil {
				break
			}
		}
		return mainLoop(loop, pipeline)
	})
	select {}
}
