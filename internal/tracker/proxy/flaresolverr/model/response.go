package model

type Response struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Solution struct {
		Url       string  `json:"url"`
		Status    int     `json:"status"`
		Cookies   Cookies `json:"cookies"`
		UserAgent string  `json:"userAgent"`
		Response  string  `json:"response"`
	} `json:"solution"`
}
