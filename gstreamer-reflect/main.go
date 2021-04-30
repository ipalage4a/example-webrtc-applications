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

var receiveDone chan struct{} = make(chan struct{}, 2)

const (
	videoClockRate = 90000
	audioClockRate = 48000
	pcmClockRate   = 8000
)

func createSrc(pipeline *gst.Pipeline) (src *gst.Element, err error) {

	src, err = gst.ElementFactoryMake("appsrc", "input")
	if err != nil {
		return
	}
	src.SetObject("format", int(gst.FormatTime))
	src.SetObject("is-live", true)
	src.SetObject("do-timestamp", true)

	rtp_to_opus, err := gst.ElementFactoryMake("rtpopusdepay", "")
	if err != nil {
		return
	}
	raw, err := gst.ElementFactoryMake("opusdecode", "")
	if err != nil {
		return
	}

	pipeline.AddMany(src, rtp_to_opus, raw)

	xrtp := gst.CapsFromString("application/x-rtp, encoding-name=VP8-DRAFT-IETF-01")
	src.LinkFiltered(rtp_to_opus, xrtp)
	rtp_to_opus.Link(raw)

	return
}

func createSink(pipeline *gst.Pipeline) (sink *gst.Element, err error) {
	opusEnc, err := gst.ElementFactoryMake("opusenc", "")
	xOpus := gst.CapsFromString("audio/x-opus")
	if err != nil {
		return
	}

	sink, err = gst.ElementFactoryMake("appsink", "output")
	if err != nil {
		return
	}

	pipeline.AddMany(sink, opusEnc)

	opusEnc.LinkFiltered(sink, xOpus)
	opusEnc.Link(sink)
	return
}

// gstreamerReceiveMain is launched in a goroutine because the main thread is needed
// for Glib's main loop (Gstreamer uses Glib)
func gstreamerReceiveMain() {
	// Everything below is the pion-WebRTC API! Thanks for using it ❤️.

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
	pipeline, err = gst.PipelineNew("")
	if err != nil {
		return
	}

	src, err = createSrc(pipeline)
	sink, err = createSink(pipeline)
	if err != nil {
		panic(err)
	}

	// codecName := "opus"

	// pipelineStr := "appsrc format=time is-live=true do-timestamp=true name=input ! application/x-rtp"

	// switch strings.ToLower(codecName) {
	// case "vp8":
	// 	pipelineStr += ", encoding-name=VP8-DRAFT-IETF-01 ! rtpvp8depay ! decodebin"
	// case "opus":
	// 	pipelineStr += ", payload=96, encoding-name=OPUS ! rtpopusdepay ! decodebin"
	// case "vp9":
	// 	pipelineStr += " ! rtpvp9depay ! decodebin"
	// case "h264":
	// 	pipelineStr += " ! rtph264depay ! decodebin"
	// case "g722":
	// 	pipelineStr += " clock-rate=8000 ! rtpg722depay ! decodebin"
	// default:
	// 	panic("Unhandled codec " + codecName)
	// }

	// pipelineStrSink := "appsink name=output"

	// var clockRate float32

	// switch strings.ToLower(codecName) {
	// case "vp8":
	// 	pipelineStr += " ! vp8enc ! video/x-vp8 ! " + pipelineStrSink
	// 	clockRate = videoClockRate

	// case "vp9":
	// 	pipelineStr += " ! vp9enc ! " + pipelineStrSink
	// 	clockRate = videoClockRate

	// case "h264":
	// 	pipelineStr += " ! video/x-raw,format=I420 ! x264enc speed-preset=ultrafast tune=zerolatency key-int-max=20 ! video/x-h264,stream-format=byte-stream ! " + pipelineStrSink
	// 	clockRate = videoClockRate

	// case "opus":
	// 	pipelineStr += " ! opusenc ! audio/x-opus ! " + pipelineStrSink
	// 	clockRate = audioClockRate

	// case "g722":
	// 	pipelineStr += " ! avenc_g722 ! " + pipelineStrSink
	// 	clockRate = audioClockRate

	// case "pcmu":
	// 	pipelineStr += " ! audio/x-raw, rate=8000 ! mulawenc ! " + pipelineStrSink
	// 	clockRate = pcmClockRate

	// case "pcma":
	// 	pipelineStr += " ! audio/x-raw, rate=8000 ! alawenc ! " + pipelineStrSink
	// 	clockRate = pcmClockRate

	// default:
	// 	panic("Unhandled codec " + codecName)
	// }

	// log.Println(pipelineStr)
	// log.Println(clockRate)
	// pipeline, err = gst.ParseLaunch(pipelineStr)

	// if err != nil {
	// 	panic(err)
	// }
	// pipeline.SetState(gst.StatePlaying)

	// src = pipeline.GetByName("input")

	// Set a handler for when a new remote track starts, this handler creates a gstreamer pipeline
	// for the given codec
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
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				fmt.Println("push ", len(buf[:i]))
			default:
				i, _, err = track.Read(buf)
				if err != nil {
					panic(err)
				}
				err = src.PushBuffer(buf[:i])
			}
		}
	})

	// Set the handler for ICE connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		fmt.Printf("Connection State has changed %s \n", connectionState.String())
	})

	// Wait for the offer to be pasted
	offer := webrtc.SessionDescription{}

	signal.Decode(signal.MustReadStdin(), &offer)

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
		var lenData int
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		go func() {
			for range ticker.C {
				fmt.Println("pull ", lenData)
			}
		}()

		for {
			if sink == nil {
				continue
			}
			out, err := sink.PullSample()
			if err != nil {
				continue
			}

			lenData = len(out.Data)

			if err := track.WriteSample(media.Sample{Data: out.Data, Duration: time.Duration(out.Duration), Timestamp: time.Unix(int64(out.Pts), 0)}); err != nil {
				panic(err)
			}
		}
	}()
	go gstreamerReceiveMain()

	select {}
}
