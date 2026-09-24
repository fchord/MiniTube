package liveorigin

import "strings"

func RewritePlaylist(body []byte, complete bool) []byte {
	s := strings.ReplaceAll(string(body), "\r\n", "\n")
	if !strings.HasPrefix(s, "#EXTM3U") {
		s = "#EXTM3U\n" + s
	}
	if strings.Contains(s, "#EXT-X-PLAYLIST-TYPE:") {
		lines := strings.Split(s, "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "#EXT-X-PLAYLIST-TYPE:") {
				if complete {
					lines[i] = "#EXT-X-PLAYLIST-TYPE:VOD"
				} else {
					lines[i] = "#EXT-X-PLAYLIST-TYPE:EVENT"
				}
			}
		}
		s = strings.Join(lines, "\n")
	} else {
		kind := "EVENT"
		if complete {
			kind = "VOD"
		}
		s = strings.Replace(s, "#EXTM3U\n", "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:"+kind+"\n", 1)
	}
	if complete {
		if !strings.Contains(s, "#EXT-X-ENDLIST") {
			if !strings.HasSuffix(s, "\n") {
				s += "\n"
			}
			s += "#EXT-X-ENDLIST\n"
		}
	} else if i := strings.Index(s, "#EXT-X-ENDLIST"); i >= 0 {
		s = s[:i] + strings.TrimPrefix(s[i:], "#EXT-X-ENDLIST")
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return []byte(s)
}
