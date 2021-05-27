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
      window.track = track
      track.enabled = false
      pc.addTrack(track, stream);
      var el = document.createElement(track.kind)
      el.srcObject = stream
      el.autoplay = false
      el.controls = true

      document.getElementById('local').appendChild(el)
    });


    pc.createOffer().then(d => pc.setLocalDescription(d)).catch(log)
  }).catch(log)

pc.oniceconnectionstatechange = e => log(pc.iceConnectionState)
pc.onicecandidate = event => {
  if (event.candidate === null) {
    let sdp = btoa(JSON.stringify(pc.localDescription))
    document.getElementById('localSessionDescription').value = sdp
    
    window.sendSdp(sdp)
  }
}

pc.ontrack = function (event) {
  var el = document.createElement(event.track.kind)
  el.srcObject = event.streams[0]
  el.autoplay = true
  el.controls = true
  el.muted = true

  document.getElementById('remote').appendChild(el)
}

window.toggleMute = () => {
  if (window.track.enabled) {
    document.body.style.backgroundColor = "red"
    window.track.enabled = false
  } else {
    document.body.style.backgroundColor = "white"
    window.track.enabled = true
  }
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

window.sendSdp = (sdp) => {
  var xhttp = new XMLHttpRequest();
  xhttp.onreadystatechange=function() {
    if (this.readyState == 4 && this.status == 200) {
      document.getElementById('remoteSessionDescription').value = this.responseText
      window.startSession()
    }
  };
  xhttp.open("POST", "http://localhost:8080/sdp", true);
  xhttp.send(sdp);
}

window.addDisplayCapture = () => {
  navigator.mediaDevices.getDisplayMedia().then(stream => {
    document.getElementById('displayCapture').disabled = true

    stream.getTracks().forEach(function (track) {
      pc.addTrack(track, stream);
      var el = document.createElement(track.kind)
      el.srcObject = stream
      el.autoplay = true
      el.controls = true

      document.getElementById('local').appendChild(el)
    });

    pc.createOffer().then(d => pc.setLocalDescription(d)).catch(log)
  })
}
