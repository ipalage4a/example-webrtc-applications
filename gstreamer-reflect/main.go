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

type peer struct {
	track *webrtc.TrackLocalStaticSample
	sink  *gst.Element
	src   *gst.Element
}

var peers []peer

var peerConnection *webrtc.PeerConnection

var pipeline *gst.Pipeline
var mixer, tee *gst.Element

// var src, sink *gst.Element

func initMuxer() (err error) {
	pipeline, err = gst.PipelineNew("test")
	if err != nil {
		return
	}

	mixer, err = gst.ElementFactoryMake("audiomixer", "")
	if err != nil {
		return
	}
	tee, err = gst.ElementFactoryMake("tee", "")
	if err != nil {
		return err
	}

	pipeline.AddMany(mixer, tee)

	mixer.Link(tee)

	return
}

func newSrc() (src *gst.Element, err error) {
	src, err = gst.ElementFactoryMake("appsrc", "")
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

	pipeline.AddMany(src, rtp_to_opus, opusDec)

	src.LinkFiltered(rtp_to_opus, xrtp)
	rtp_to_opus.Link(opusDec)

	rawSrcPad := opusDec.GetStaticPad("src")

	pdTemplate := mixer.GetPadTemplate("sink_%u")
	mixerSinkPad := mixer.GetRequestPad(pdTemplate, "", nil)
	rawSrcPad.Link(mixerSinkPad)

	return
}

func newSink() (sink *gst.Element, err error) {

	opusEnc, err := gst.ElementFactoryMake("opusenc", "")

	xOpus := gst.CapsFromString("audio/x-opus")
	if err != nil {
		return
	}

	sink, err = gst.ElementFactoryMake("appsink", "")
	if err != nil {
		return
	}

	pipeline.AddMany(opusEnc, sink)

	pdTemplate := tee.GetPadTemplate("src_%u")
	teePad := tee.GetRequestPad(pdTemplate, "", nil)
	
	opusEncPad := opusEnc.GetStaticPad("sink")

	teePad.Link(opusEncPad)

	opusEnc.LinkFiltered(sink, xOpus)
	return
}

// gstreamerReceiveMain is launched in a goroutine because the main thread is needed
// for Glib's main loop (Gstreamer uses Glib)
func gstreamerReceiveMain() {
	// Everything below is the pion-WebRTC API! Thanks for using it ❤️.

	var err error
	err = initMuxer()

	if err != nil {
		panic(err)
	}

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	sdpChan := signal.HTTPSDPServer()

	for {

		// Wait for the offer to be pasted
		offer := webrtc.SessionDescription{}

		// signal.Decode(signal.MustReadStdin(), &offer)
		signal.Decode(<-sdpChan, &offer)

		go func() {
			// Create a new RTCPeerConnection
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

			// Set a handler for when a new remote track starts, this handler creates a gstreamer pipeline
			// for the given codec

			src, err := newSrc()
			sink, err := newSink()
			pipeline.SetState(gst.StatePlaying)

			p := peer{track, sink, src}
			peers = append(peers, p)

			peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
				buf := make([]byte, 1400)
				var i int

				// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
				go func() {
					ticker := time.NewTicker(time.Second * 3)
					for range ticker.C {
						fmt.Println(buf[:i])
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
					err = src.PushBuffer(buf[:i])
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
		}()

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
					fmt.Println("pull ", debug)
				}
			}
		}()

		for {
			for _, p := range peers {
				out, err = p.sink.PullSample()
				if err != nil {
					continue
				}
				debug = out

				if err := p.track.WriteSample(media.Sample{Data: out.Data, Duration: time.Duration(out.Duration), Timestamp: time.Unix(int64(out.Pts), 0)}); err != nil {
					panic(err)
				}
			}
		}
	}()

	go gstreamerReceiveMain()

	select {}
}
