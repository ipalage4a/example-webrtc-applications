/* eslint-env browser */

let pc = new RTCPeerConnection({
  iceServers: [
    {
      urls: 'stun:stun.l.google.com:19302'
    }
  ]
})
let log = msg => {
  document.getElementById('logs').innerHTML += msg + '<br>'
}


navigator.mediaDevices.getUserMedia({ video: false, audio: true })
  .then(stream => {

    stream.getTracks().forEach(function (track) {
      pc.addTrack(track, stream);
      var el = document.createElement(track.kind)
      el.srcObject = stream
      el.autoplay = true
      el.controls = true

      document.getElementById('local').appendChild(el)
    });

    // displayVideo(stream)


    pc.createOffer().then(d => pc.setLocalDescription(d)).catch(log)
  }).catch(log)

pc.oniceconnectionstatechange = e => log(pc.iceConnectionState)
pc.onicecandidate = event => {
  if (event.candidate === null) {
    document.getElementById('localSessionDescription').value = btoa(JSON.stringify(pc.localDescription))
  }
}

pc.ontrack = function (event) {
  var el = document.createElement(event.track.kind)
  el.srcObject = event.streams[0]
  el.autoplay = true
  el.controls = true

  document.getElementById('remote').appendChild(el)
}


window.startSession = () => {
  let sd = document.getElementById('remoteSessionDescription').value
  if (sd === '') {
    return alert('Session Description must not be empty')
  }

  try {
    pc.setRemoteDescription(new RTCSessionDescription(JSON.parse(atob(sd))))
  } catch (e) {
    alert(e)
  }
}

window.addDisplayCapture = () => {
  navigator.mediaDevices.getDisplayMedia().then(stream => {
    document.getElementById('displayCapture').disabled = true

    stream.getTracks().forEach(function (track) {
      pc.addTrack(track, displayVideo(stream));
    });

    pc.createOffer().then(d => pc.setLocalDescription(d)).catch(log)
  })
}
