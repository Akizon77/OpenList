package sign

func SignEmby(path, resource string) string {
	return Sign(embySignData(path, resource))
}

func VerifyEmby(path, resource, signature string) error {
	return Verify(embySignData(path, resource), signature)
}

func embySignData(path, resource string) string {
	return "emby-proxy:" + path + "\x00" + resource
}
