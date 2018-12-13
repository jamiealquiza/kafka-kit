package server

func mockServer() *Server {
	s, _ := NewServer(ServerConfig{
		ReadReqRate:  1,
		WriteReqRate: 1,
		mock:         true,
	})

	s.DialZK(nil, nil, nil)

	return s
}

func intsEqual(s1, s2 []uint32) bool {
	if len(s1) != len(s2) {
		return false
	}

	for i := range s1 {
		if s1[i] != s2[i] {
			return false
		}
	}

	return true
}

func stringsEqual(s1, s2 []string) bool {
	if len(s1) != len(s2) {
		return false
	}

	for i := range s1 {
		if s1[i] != s2[i] {
			return false
		}
	}

	return true
}
