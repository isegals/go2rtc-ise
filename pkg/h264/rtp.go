package h264

import (
	"encoding/binary"
	"github.com/AlexxIT/go2rtc/pkg/streamer"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

const RTPPacketVersionAVC = 0

func RTPDepay(track *streamer.Track) streamer.WrapperFunc {
	depack := &codecs.H264Packet{IsAVC: true}

	sps, pps := GetParameterSet(track.Codec.FmtpLine)
	ps := EncodeAVC(sps, pps)

	buf := make([]byte, 0, 512*1024) // 512K

	return func(push streamer.WriterFunc) streamer.WriterFunc {
		return func(packet *rtp.Packet) error {
			//fmt.Printf(
			//	"[RTP] codec: %s, nalu: %2d, size: %6d, ts: %10d, pt: %2d, ssrc: %d, seq: %d, %v\n",
			//	track.Codec.Name, packet.Payload[0]&0x1F, len(packet.Payload), packet.Timestamp,
			//	packet.PayloadType, packet.SSRC, packet.SequenceNumber, packet.Marker,
			//)

			payload, err := depack.Unmarshal(packet.Payload)
			if len(payload) == 0 || err != nil {
				return nil
			}

			// Fix TP-Link Tapo TC70: sends SPS and PPS with packet.Marker = true
			if packet.Marker {
				switch NALUType(payload) {
				case NALUTypeSPS, NALUTypePPS:
					buf = append(buf, payload...)
					return nil
				}
			}

			if len(buf) == 0 {
				switch NALUType(payload) {
				case NALUTypeIFrame:
					// fix IFrame without SPS,PPS
					buf = append(buf, ps...)
				case NALUTypeSEI:
					// fix ffmpeg with transcoding first frame
					i := 4 + binary.BigEndian.Uint32(payload)
					payload = payload[i:]
					if NALUType(payload) == NALUTypeIFrame {
						buf = append(buf, ps...)
					}
				}
			}

			// collect all NALs for Access Unit
			if !packet.Marker {
				buf = append(buf, payload...)
				return nil
			}

			if len(buf) > 0 {
				payload = append(buf, payload...)
				buf = buf[:0]
			}

			//fmt.Printf(
			//	"[AVC] %v, len: %d, %v\n", Types(payload), len(payload),
			//	reflect.ValueOf(buf).Pointer() == reflect.ValueOf(payload).Pointer(),
			//)

			clone := *packet
			clone.Version = RTPPacketVersionAVC
			clone.Payload = payload
			return push(&clone)
		}
	}
}

func RTPPay(mtu uint16) streamer.WrapperFunc {
	payloader := &Payloader{IsAVC: true}
	sequencer := rtp.NewRandomSequencer()
	mtu -= 12 // rtp.Header size

	return func(push streamer.WriterFunc) streamer.WriterFunc {
		return func(packet *rtp.Packet) error {
			if packet.Version == RTPPacketVersionAVC {
				payloads := payloader.Payload(mtu, packet.Payload)
				last := len(payloads) - 1
				for i, payload := range payloads {
					clone := rtp.Packet{
						Header: rtp.Header{
							Version: 2,
							Marker:  i == last,
							//PayloadType:    packet.PayloadType,
							SequenceNumber: sequencer.NextSequenceNumber(),
							Timestamp:      packet.Timestamp,
							//SSRC:           packet.SSRC,
						},
						Payload: payload,
					}
					if err := push(&clone); err != nil {
						return err
					}
				}
				return nil
			}

			return push(packet)
		}
	}
}
