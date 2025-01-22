# matrix-qq
A Matrix-QQ puppeting bridge based on [LagrangeGo](https://github.com/LagrangeDev/LagrangeGo) and [mautrix-go](https://github.com/mautrix/go).

### Documentation

Some quick links:

* [Bridge setup](https://docs.mau.fi/bridges/go/setup.html)
* [Docker](https://hub.docker.com/r/lxduo/matrix-qq)
* [Step by Step (Chinese)](https://duo.github.io/posts/matrix-qq-wechat/)

### Features & roadmap

* Matrix → QQ
  * [ ] Message types
    * [x] Text
    * [x] Image
    * [x] Sticker
    * [x] Video
    * [ ] Audio
    * [ ] File
    * [x] Mention
    * [x] Reply
    * [x] Location
  * [x] Chat types
	  * [x] Direct
	  * [x] Room
  * [ ] Presence
  * [x] Redaction
  * [ ] Group actions
    * [ ] Join
    * [ ] Invite
    * [ ] Leave
    * [ ] Kick
    * [ ] Mute
  * [ ] Room metadata
    * [ ] Name
    * [ ] Avatar
    * [ ] Topic
  * [ ] User metadata
    * [ ] Name
    * [ ] Avatar

* QQ → Matrix
  * [ ] Message types
    * [x] Text
    * [x] Image
    * [ ] Sticker
    * [x] Video
    * [ ] Audio
    * [x] File
    * [x] Mention
    * [x] Reply
    * [x] Location
  * [ ] Chat types
    * [x] Private
    * [x] Group
    * [ ] Stranger (unidirectional)
  * [ ] Presence
  * [x] Redaction
  * [ ] Group actions
    * [ ] Invite
    * [x] Join
    * [x] Leave
    * [x] Kick
    * [ ] Mute
  * [ ] Group metadata
    * [x] Name
    * [x] Avatar
	  * [x] Topic
  * [x] User metadata
    * [x] Name
    * [x] Avatar
  * [ ] Login types
	  * [ ] Password
	  * [x] QR code

* Misc
  * [ ] Automatic portal creation
    * [ ] After login
    * [ ] When added to group
    * [x] When receiving message
  * [x] Double puppeting
