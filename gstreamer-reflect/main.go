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

type peer struct {
	track *webrtc.TrackLocalStaticSample
	sink  *gst.Element
	src   *gst.Element

	tee   *gst.Element
	queue *gst.Element
}

var listLock sync.Mutex
var peers map[int]*peer = make(map[int]*peer)

var peerConnection *webrtc.PeerConnection
var config webrtc.Configuration

var pipeline *gst.Pipeline

func initPipeline() (err error) {
	pipeline, err = gst.PipelineNew("test")
	return
}

func initPeer(i int) (p *peer, err error) {
	listLock.Lock()
	defer listLock.Unlock()

	peerConnection, err = webrtc.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}

	// Create a audio track
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: "audio/opus"}, "audio", "pion1")
	if err != nil {
		panic(err)
	}

	_, err = peerConnection.AddTrack(track)
	if err != nil {
		panic(err)
	}

	src, err := gst.ElementFactoryMake("appsrc", "")
	if err != nil {
		return
	}
	src.SetObject("format", int(gst.FormatTime))
	src.SetObject("is-live", true)
	src.SetObject("do-timestamp", true)

	xrtp := gst.CapsFromString("application/x-rtp, payload=96, encoding-name=OPUS")

	rtp_to_opus, err := gst.ElementFactoryMake("rtpopusdepay", "")
	if err != nil {
		return
	}

	opusDec, err := gst.ElementFactoryMake("opusdec", "")
	if err != nil {
		return
	}

	tee, err := gst.ElementFactoryMake("tee", "")
	if err != nil {
		return
	}

	queue, err := gst.ElementFactoryMake("queue", "")
	if err != nil {
		return
	}

	convert, err := gst.ElementFactoryMake("audioconvert", "")
	if err != nil {
		return
	}

	mixer, err := gst.ElementFactoryMake("audiomixer", "")
	if err != nil {
		return
	}

	opusEnc, err := gst.ElementFactoryMake("opusenc", "")

	xOpus := gst.CapsFromString("audio/x-opus")
	if err != nil {
		return
	}

	sink, err := gst.ElementFactoryMake("appsink", "")
	if err != nil {
		return
	}

	pipeline.AddMany(src, rtp_to_opus, opusDec, mixer, tee, opusEnc, sink, convert, queue)

	src.LinkFiltered(rtp_to_opus, xrtp)
	rtp_to_opus.Link(opusDec)

	opusDec.Link(tee)

	queue.Link(convert)
	convert.Link(mixer)
	mixer.Link(opusEnc)

	opusEnc.LinkFiltered(sink, xOpus)

	for _, peer := range peers {
		selfTeePadTpl := tee.GetPadTemplate("src_%u")
		selfTeePad := tee.GetRequestPad(selfTeePadTpl, "", nil)
		peerTeePad := peer.queue.GetStaticPad("sink")
		selfTeePad.Link(peerTeePad)

		otherTeePadTpl := peer.tee.GetPadTemplate("src_%u")
		otherTeePad := tee.GetRequestPad(otherTeePadTpl, "", nil)
		queuePad := queue.GetStaticPad("sink")
		otherTeePad.Link(queuePad)
	}

	p = &peer{
		track: track,
		sink:  sink,
		src:   src,
		tee:   tee,
		queue: queue,
	}

	pipeline.SetState(gst.StatePlaying)

	peers[i] = p
	return
}

// gstreamerReceiveMain is launched in a goroutine because the main thread is needed
// for Glib's main loop (Gstreamer uses Glib)
func gstreamerReceiveMain() {
	// Everything below is the pion-WebRTC API! Thanks for using it ❤️.

	err := initPipeline()

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

	var i int
	for {

		// Wait for the offer to be pasted
		offer := webrtc.SessionDescription{}

		// signal.Decode(signal.MustReadStdin(), &offer)
		signal.Decode(<-sdpChan, &offer)
		i++

		p, err := initPeer(i)

		peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
			fmt.Printf("OnTrack fired on %d peer\n", i)

			buf := make([]byte, 1400)
			var i int

			// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
			go func() {
				ticker := time.NewTicker(time.Second * 3)
				for range ticker.C {
					rtcpSendErr := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}})
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
			}

		})

		// Set the handler for ICE connection state
		// This will notify you when the peer has connected/disconnected
		peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
			fmt.Printf("Connection State has changed %s \n", connectionState.String())
		})

		// Set the remote SessionDescription
		err = peerConnection.SetRemoteDescription(offer)
		if err != nil {
			panic(err)
		}

		// Create an answer
		answer, err := peerConnection.CreateAnswer(nil)
		if err != nil {
			panic(err)
		}

		// Create channel that is blocked until ICE Gathering is complete
		gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

		// Sets the LocalDescription, and starts our UDP listeners
		err = peerConnection.SetLocalDescription(answer)
		if err != nil {
			panic(err)
		}

		// Block until ICE Gathering is complete, disabling trickle ICE
		// we do this because we only can exchange one signaling message
		// in a production application you should exchange ICE Candidates via OnICECandidate
		<-gatherComplete

		// Output the answer in base64 so we can paste it in browser
		fmt.Println(signal.Encode(*peerConnection.LocalDescription()))
		localSdp <- signal.Encode(*peerConnection.LocalDescription())
	}
}

func main() {
	// Start a new thread to do the actual work for this application
	go func() {
		var err error
		var out *gst.Sample
		var debug interface{}
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		go func() {
			for range ticker.C {
				if err != nil {
					fmt.Println("error: ", err)
				} else {
					listLock.Lock()
					ps := peers
					listLock.Unlock()

					fmt.Println("peers:", ps)
					fmt.Println("pull ", debug)
				}
			}
		}()

		for {
			listLock.Lock()
			ps := peers
			listLock.Unlock()

			for k, p := range ps {
				out, err = p.sink.PullSample()

				debug = fmt.Sprint(k, ": ", out)

				if err != nil {
					continue
				}

				if err := p.track.WriteSample(media.Sample{Data: out.Data, Duration: time.Duration(out.Duration), Timestamp: time.Unix(int64(out.Pts), 0)}); err != nil {
					panic(err)
				}
			}
		}
	}()

	go gstreamerReceiveMain()
	select {}
}
