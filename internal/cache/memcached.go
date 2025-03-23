package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/IliaW/rule-api/config"
	"github.com/IliaW/rule-api/util"
	"github.com/bradfitz/gomemcache/memcache"
)

//go:generate go run github.com/vektra/mockery/v2@v2.53.0 --name CachedClient
type CachedClient interface {
	GetRobotsFile(string) ([]byte, bool)
	SaveRobotsFile(string, []byte)
	Close()
}

type MemcachedClient struct {
	client *memcache.Client
	cfg    *config.CacheConfig
}

func NewMemcachedClient(cacheConfig *config.CacheConfig) *MemcachedClient {
	slog.Info("connecting to memcached...")
	ss := new(memcache.ServerList)
	err := ss.SetServers(cacheConfig.Servers...)
	if err != nil {
		slog.Error("failed to set memcached servers.", slog.String("err", err.Error()))
		os.Exit(1)
	}
	c := &MemcachedClient{
		client: memcache.NewFromSelector(ss),
		cfg:    cacheConfig,
	}
	slog.Info("pinging the memcached.")
	err = c.client.Ping()
	if err != nil {
		slog.Error("connection to the memcached is failed.", slog.String("err", err.Error()))
		os.Exit(1)
	}
	slog.Info("connected to memcached!")

	return c
}

func (mc *MemcachedClient) GetRobotsFile(url string) ([]byte, bool) {
	key := mc.generateDomainHash(url)
	item, err := mc.client.Get(key)
	if err != nil {
		if errors.Is(err, memcache.ErrCacheMiss) {
			slog.Debug("cache not found.", slog.String("key", key), slog.String("url", url))
			return nil, false
		} else {
			slog.Error("failed to check if cached.", slog.String("key", key), slog.String("url", url),
				slog.String("err", err.Error()))
			return nil, false
		}
	}
	slog.Debug("cache found.", slog.String("key", key))

	return item.Value, true
}
func (mc *MemcachedClient) SaveRobotsFile(url string, robotFile []byte) {
	key := mc.generateDomainHash(url)
	if err := mc.set(key, robotFile, int32((mc.cfg.TtlForRobotsTxt).Seconds())); err != nil {
		slog.Error("failed to save robots file to cache.", slog.String("key", key),
			slog.String("err", err.Error()))
		return
	}
	slog.Debug("robots file saved to cache.")
}

func (mc *MemcachedClient) Close() {
	slog.Info("closing memcached connection.")
	err := mc.client.Close()
	if err != nil {
		slog.Error("failed to close memcached connection.", slog.String("err", err.Error()))
	}
}

func (mc *MemcachedClient) set(key string, value any, expiration int32) error {
	byteValue, err := json.Marshal(value)
	if err != nil {
		return err
	}
	item := &memcache.Item{
		Key:        key,
		Value:      byteValue,
		Expiration: expiration,
	}

	return mc.client.Set(item)
}

func (mc *MemcachedClient) generateDomainHash(url string) string {
	var key string
	domain, err := util.GetDomain(url)
	if err != nil {
		slog.Error("failed to parse url. Use full url as a key.", slog.String("url", url),
			slog.String("err", err.Error()))
		key = fmt.Sprintf("%s-robots-txt", hashURL(url))
	} else {
		key = fmt.Sprintf("%s-robots-txt", hashURL(domain))
		slog.Debug("key created.", slog.String("key:", key))
	}

	return key
}

func hashURL(url string) string {
	hash := sha256.New()
	hash.Write([]byte(url))
	return hex.EncodeToString(hash.Sum(nil))
}
