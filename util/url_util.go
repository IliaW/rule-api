package util

import (
	"errors"
	u "net/url"
)

func GetDomain(url string) (string, error) {
	parsedUrl, err := u.Parse(url)
	if err != nil {
		return "", err
	}
	if parsedUrl.Hostname() == "" {
		return "", errors.New("invalid url. Url should contain scheme and hostname")
	}

	return parsedUrl.Hostname(), nil
}

func GetBaseUrl(url string) (string, error) {
	parsedUrl, err := u.Parse(url)
	if err != nil {
		return "", err
	}
	if parsedUrl.Scheme == "" || parsedUrl.Hostname() == "" {
		return "", errors.New("invalid url. Url should contain scheme and hostname")
	}

	return parsedUrl.Scheme + "://" + parsedUrl.Hostname(), nil
}
