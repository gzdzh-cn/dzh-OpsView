package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"log"
	"opsview/internal/catalog"
	"opsview/internal/security"
	"os"
)

func main() {
	mode := flag.String("mode", "sign", "keygen 或 sign")
	keyPath := flag.String("key", "", "私钥路径")
	input := flag.String("input", "", "清单 JSON")
	id := flag.String("id", "", "签名者 ID")
	out := flag.String("output", "", "输出文件")
	flag.Parse()
	if *mode == "keygen" {
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			log.Fatal(err)
		}
		if err = security.WriteAtomic(*keyPath, []byte(base64.StdEncoding.EncodeToString(key)), 0600); err != nil {
			log.Fatal(err)
		}
		if err = security.WriteAtomic(*keyPath+".pub", []byte(base64.StdEncoding.EncodeToString(pub)), 0644); err != nil {
			log.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		log.Fatal(err)
	}
	raw, err := os.ReadFile(*keyPath)
	if err != nil {
		log.Fatal(err)
	}
	key, err := base64.StdEncoding.DecodeString(string(raw))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		log.Fatal("invalid private key")
	}
	var valid any
	if json.Unmarshal(data, &valid) != nil {
		log.Fatal("清单不是有效 JSON")
	}
	env := catalog.Envelope{KeyID: *id, Payload: base64.StdEncoding.EncodeToString(data), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, data))}
	b, _ := json.MarshalIndent(env, "", "  ")
	if err = security.WriteAtomic(*out, b, 0644); err != nil {
		log.Fatal(err)
	}
}
