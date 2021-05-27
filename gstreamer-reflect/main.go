package main

import (
	"C"
	"fmt"
	"sync"
	"time"

	"github.com/ipalage4a/gst"
	"github.com/pion/example-webrtc-applications/v3/internal/signal"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)
import "sort"

type peer struct {
	peer   *webrtc.PeerConnection
	track  *webrtc.TrackLocalStaticSample
	src    *gst.Element
	buzzer *gst.Element
	sink   *gst.Element
	queue  *gst.Element
	tee    *gst.Element
	mixer  *gst.Element

	opusDecSrc *gst.Pad
	buzzerSrc  *gst.Pad
	teeSink    *gst.Pad
	mixerSink  *gst.Pad

	debug struct {
		push string
		pull interface{}
	}
}

func (p *peer) teeToPeer(other *peer) {
	var src *gst.Pad

	srcTpl := p.tee.GetPadTemplate("src_%u")
	src = p.tee.GetRequestPad(srcTpl, "", nil)

	sinkTpl := other.mixer.GetPadTemplate("sink_%u")
	sink := other.mixer.GetRequestPad(sinkTpl, "", nil)

	src.Link(sink)

	pipeline.SetState(gst.StatePlaying)
}

func (p *peer) cancelBuzz() {
	if p.buzzer != nil {
		p.buzzerSrc.Unlink(p.mixerSink)
		p.opusDecSrc.Link(p.teeSink)
		pipeline.Remove(p.buzzer)
		p.buzzer = nil
	}
}

var listLock sync.Mutex
var peers map[int]*peer = make(map[int]*peer)

var config webrtc.Configuration

type pip struct {
	*gst.Pipeline
	state gst.StateOptions
}

func (p *pip) SetState(state gst.StateOptions) {
	// if p.state != state {
	p.state = state
	p.Pipeline.SetState(state)
	// }
}

var pipeline = pip{}

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
	p, err := gst.PipelineNew("test")
	if err != nil {
		panic(err)
	}

	pipeline.Pipeline = p
	pipeline.SetState(gst.StatePlaying)
	return
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

	src, err := gst.ElementFactoryMake("appsrc", "")
	if err != nil {
		return
	}

	src.SetObject("format", int(gst.FormatTime))
	src.SetObject("is-live", true)
	src.SetObject("do-timestamp", true)
	// src.SetObject("min-latency", int(time.Millisecond*500))
	// src.SetObject("max-latency", int(time.Millisecond*1000))
	srcQueue, err := gst.ElementFactoryMake("queue2", "")
	if err != nil {
		return
	}

	xrtp := gst.CapsFromString("application/x-rtp, payload=96, encoding-name=OPUS")

	rtp_to_opus, err := gst.ElementFactoryMake("rtpopusdepay", "")
	if err != nil {
		return
	}

	opusDec, err := gst.ElementFactoryMake("opusdec", "")
	if err != nil {
		return
	}
	opusDecSrc := opusDec.GetStaticPad("src")

	tee, err := gst.ElementFactoryMake("tee", "")
	if err != nil {
		return
	}
	teeSink := tee.GetStaticPad("sink")

	srcConvert, err := gst.ElementFactoryMake("audioconvert", "")
	if err != nil {
		return
	}

	sinkConvert, err := gst.ElementFactoryMake("audioconvert", "")
	if err != nil {
		return
	}

	mixer, err := gst.ElementFactoryMake("audiomixer", "")
	if err != nil {
		return
	}

	opusEnc, err := gst.ElementFactoryMake("opusenc", "")
	if err != nil {
		return
	}

	xOpus := gst.CapsFromString("audio/x-opus")
	if err != nil {
		return
	}

	sink, err := gst.ElementFactoryMake("appsink", "")
	if err != nil {
		return
	}

	sinkQueue, err := gst.ElementFactoryMake("queue2", "")
	if err != nil {
		return
	}

	p = &peer{
		peer:  peerConnection,
		track: track,
		src:   src,
		sink:  sink,
		tee:   tee,
		mixer: mixer,

		opusDecSrc: opusDecSrc,
		teeSink:    teeSink,
	}

	pipeline.AddMany(src, srcQueue, rtp_to_opus, opusDec, mixer, tee, opusEnc, sinkQueue, sink, srcConvert, sinkConvert)

	src.Link(srcQueue)
	srcQueue.LinkFiltered(rtp_to_opus, xrtp)
	rtp_to_opus.Link(opusDec)
	opusDec.Link(tee)

	for key, peer := range getPeers() {
		fmt.Printf("\n\nconnect %d to %d peer\n\n", i, key)
		p.teeToPeer(peer)
		fmt.Printf("\n\nconnect %d to %d peer\n\n", key, i)
		peer.teeToPeer(p)
	}

	mixer.Link(sinkQueue)
	sinkQueue.Link(sinkConvert)
	sinkConvert.Link(opusEnc)
	opusEnc.LinkFiltered(sink, xOpus)

	// src.SetState(gst.StatePlaying)
	// srcQueue.SetState(gst.StatePlaying)
	// rtp_to_opus.SetState(gst.StatePlaying)
	// opusDec.SetState(gst.StatePlaying)
	// mixer.SetState(gst.StatePlaying)
	// tee.SetState(gst.StatePlaying)
	// opusEnc.SetState(gst.StatePlaying)
	// sink.SetState(gst.StatePlaying)
	// sinkQueue.SetState(gst.StatePlaying)
	// mixer.SetState(gst.StatePlaying)
	// srcConvert.SetState(gst.StatePlaying)
	// sinkConvert.SetState(gst.StatePlaying)

	addPeer(i, p)

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
	sdpChan := signal.HTTPSDPServerWithAnswer(localSdp)

	var peerKey int
	for {

		// Wait for the offer to be pasted
		offer := webrtc.SessionDescription{}

		// signal.Decode(signal.MustReadStdin(), &offer)
		signal.Decode(<-sdpChan, &offer)

		peerKey++

		p, err := initPeer(peerKey)

		p.peer.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
			fmt.Printf("OnTrack fired on %d peer\n", peerKey)

			buf := make([]byte, 1500)
			var i int

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

			for {
				i, _, err = track.Read(buf)
				if err != nil {
					panic(err)
				}

				if err := p.src.PushBuffer(buf[:i]); err != nil {
					panic(err)
				}

				p.debug.push = fmt.Sprint(i)
			}

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
		fmt.Println(signal.Encode(*p.peer.LocalDescription()))
		localSdp <- signal.Encode(*p.peer.LocalDescription())
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
					fmt.Printf("\tpush: %s\n", ps[k].debug.push)
					fmt.Printf("\tpull: %v\n", ps[k].debug.pull)
				}
				fmt.Printf("--------------\n")
			}
		}
	}()

	go func() {
		var err error
		var out *gst.Sample

		for {
			for _, p := range getPeers() {
				out, err = p.sink.PullSample()
				if err != nil {
					continue
				}

				sample := media.Sample{Data: out.Data, Duration: time.Duration(out.Duration), Timestamp: time.Unix(0, int64(out.Pts))}
				p.debug.pull = sample.Timestamp

				if err := p.track.WriteSample(sample); err != nil {
					panic(err)
				}
			}
		}
	}()

	go gstreamerReceiveMain()
	select {}
}
