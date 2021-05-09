package main

import (
	"C"
	"fmt"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"

	"github.com/ipalage4a/gst"

	"github.com/pion/example-webrtc-applications/v3/internal/signal"
)

var peerConnection *webrtc.PeerConnection
var track *webrtc.TrackLocalStaticSample

var pipeline *gst.Pipeline

var src, sink *gst.Element

var lastSrcElem, firstSinkElem *gst.Element

var receiveDone chan struct{} = make(chan struct{}, 2)

func initPipeline() (err error) {

	pipeline, err = gst.PipelineNew("test")
	if err != nil {
		return
	}

	src, err = gst.ElementFactoryMake("appsrc", "input")
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

	bin, err := gst.ElementFactoryMake("opusdec", "")
	if err != nil {
		return
	}

	opusEnc, err := gst.ElementFactoryMake("opusenc", "")

	xOpus := gst.CapsFromString("audio/x-opus")
	if err != nil {
		return
	}

	sink, err = gst.ElementFactoryMake("appsink", "output")
	if err != nil {
		return
	}

	pipeline.AddMany(src, rtp_to_opus, bin, opusEnc, sink)

	src.LinkFiltered(rtp_to_opus, xrtp)
	rtp_to_opus.Link(bin)

	bin.Link(opusEnc)

	rtp_to_opus.LinkFiltered(sink, xOpus)

	opusEnc.LinkFiltered(sink, xOpus)

	return
}

// gstreamerReceiveMain is launched in a goroutine because the main thread is needed
// for Glib's main loop (Gstreamer uses Glib)
func gstreamerReceiveMain() {
	// Everything below is the pion-WebRTC API! Thanks for using it ❤️.

	sdpChan := signal.HTTPSDPServer()

	// Prepare the configuration
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	var err error
	// Create a new RTCPeerConnection
	peerConnection, err = webrtc.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}

	// Create a audio track
	track, err = webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: "audio/opus"}, "audio", "pion1")
	if err != nil {
		panic(err)
	}
	_, err = peerConnection.AddTrack(track)
	if err != nil {
		panic(err)
	}

	// trackName := "audio"

	err = initPipeline()

	// pipelineStr := "appsrc format=time is-live=true do-timestamp=true name=input ! application/x-rtp"

	// pipelineStr += ", payload=96, encoding-name=OPUS ! rtpopusdepay ! opusdec"

	// pipelineStrSink := "appsink name=output"

	// pipelineStr += " ! opusenc ! audio/x-opus ! " + pipelineStrSink

	// pipeline, err = gst.ParseLaunch(pipelineStr)

	if err != nil {
		panic(err)
	}

	src = pipeline.GetByName("input")
	sink = pipeline.GetByName("output")

	// Set a handler for when a new remote track starts, this handler creates a gstreamer pipeline
	// for the given codec

	pipeline.SetState(gst.StatePlaying)

	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
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

		buf := make([]byte, 1400)
		var i int

		for {

			i, _, err = track.Read(buf)
			if err != nil {
				panic(err)
			}
			err = src.PushBuffer(buf[:i])
			fmt.Println("push ", len(buf[:i]))
		}

	})

	// Set the handler for ICE connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		fmt.Printf("Connection State has changed %s \n", connectionState.String())
	})

	// Wait for the offer to be pasted
	offer := webrtc.SessionDescription{}

	// signal.Decode(signal.MustReadStdin(), &offer)
	signal.Decode(<-sdpChan, &offer)

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

	// Block forever
	select {}
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
					fmt.Println("pull ", debug)
				}
			}
		}()

		for {
			if sink == nil {
				continue
			}

			out, err = sink.PullSample()
			if err != nil {
				continue
			}
			debug = out

			if err := track.WriteSample(media.Sample{Data: out.Data, Duration: time.Duration(out.Duration), Timestamp: time.Unix(int64(out.Pts), 0)}); err != nil {
				panic(err)
			}
		}
	}()

	go gstreamerReceiveMain()

	select {}
}
