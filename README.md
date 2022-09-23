# matrix-qq
A Matrix-QQ puppeting bridge based on [MiraiGo](https://github.com/Mrs4s/MiraiGo) and [mautrix-go](https://github.com/mautrix/go).

### Documentation

Some quick links:

* [Bridge setup](https://docs.mau.fi/bridges/go/setup.html)
* [Docker](https://hub.docker.com/r/lxduo/matrix-qq)

### Features & roadmap

* Matrix → QQ
  * [ ] Message types
    * [x] Text
	* [x] Image
	* [x] Sticker
	* [x] Video
	* [x] Audio
    * [x] File
    * [x] Mention
    * [x] Reply
    * [x] Location
  * [x] Chat types
	* [x] Direct
	* [x] Room
  * [ ] Presence
  * [ ] Redaction
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
	* [x] Sticker
	* [x] Video
	* [x] Audio
    * [x] File
    * [x] Mention
    * [x] Reply
    * [x] Location
  * [ ] Chat types
    * [x] Private
    * [x] Group
    * [ ] Stranger (unidirectional)
  * [ ] Presence
  * [ ] Redaction
  * [ ] Group actions
    * [ ] Invite
    * [x] Join
    * [x] Leave
    * [x] Kick
	* [ ] Mute
  * [ ] Group metadata
    * [x] Name
    * [x] Avatar
	* [ ] Topic
  * [x] User metadata
    * [x] Name
    * [x] Avatar
  * [x] Login types
	* [x] Password
	* [x] QR code

* Misc
  * [ ] Automatic portal creation
    * [ ] After login
    * [ ] When added to group
    * [x] When receiving message
  * [x] Double puppeting
