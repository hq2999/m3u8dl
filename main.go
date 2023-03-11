package main

import (
	"container/list"
	"crypto/aes"
	"crypto/cipher"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	proxy_ "golang.org/x/net/proxy"
)

const (
	ABSOLUTE int = 0
	URL      int = 1
	RELATIVE int = 2
)

const (
	RESOURCE int = 0
	INDEX    int = 1
)

type UrlStruct struct {
	url_type  int
	header    string
	long_url  string
	short_url string
	file_name string
}

type M3u8Struct struct {
	m3u8_type      int
	resource_list  []string
	resource_count int
	is_encrypt     bool
	encrypt_method string
	encrypt_key    []byte
	ext_name       string
}

var (
	lock          sync.Mutex
	hc            *http.Client
	m3u8_url      string
	retries_times int
	task_count    int
	output        string
	timeout       int
	limit         int
	proxy         string
)

var (
	downloaded_count int = 0
	file_count_      int = 0
)

var (
	m3u8_long_url  string = ""
	m3u8_short_url string = ""
)

func main() {

	flag.StringVar(&m3u8_url, "url", "", "m3u8's url")
	flag.IntVar(&retries_times, "rt", 5, "retries times")
	flag.IntVar(&task_count, "tc", 20, "task count")
	flag.StringVar(&output, "output", "output", "output filename")
	flag.IntVar(&timeout, "timeout", 15, "http timeout (second)")
	flag.IntVar(&limit, "limit", 0, "download file count limit")
	flag.StringVar(&proxy, "proxy", "", "proxy url")

	flag.Parse()

	if m3u8_url == "" {
		fmt.Printf("example: m3u8dl -url=https://......m3u8, more info: m3u8dl -h")
		return
	}

	fmt.Printf("m3u8's url: %s\n", m3u8_url)
	fmt.Printf("retries times: %d\n", retries_times)
	fmt.Printf("task count: %d\n", task_count)
	fmt.Printf("output filename: %s\n", output)
	fmt.Printf("timeout: %d(s)\n", timeout)
	fmt.Printf("proxy: %s\n", proxy)

	hc = &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
	}

	if len(proxy) > 0 {

		if strings.HasPrefix(proxy, "http") {
			fmt.Printf("proxy: %s\n", proxy)
			proxyURL, err := url.Parse(proxy)

			if err != nil {
				panic(err)
			}

			tansport := &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			}

			hc.Transport = tansport
		}

		if strings.HasPrefix(proxy, "socks5") {
			proxy = proxy[9:]
			dialer, err := proxy_.SOCKS5("tcp", proxy, nil, proxy_.Direct)
			if err != nil {
				fmt.Fprintln(os.Stderr, "can't connect to the proxy:", err)
				os.Exit(1)
			}
			// setup a http client
			httpTransport := &http.Transport{}
			hc = &http.Client{Transport: httpTransport}
			// set our socks5 as the dialer
			httpTransport.Dial = dialer.Dial
		}

	}

	if limit == 0 {
		fmt.Println("download file count limit: max")
	} else {
		fmt.Printf("download file count limit: %d\n", limit)
	}

	fmt.Println("--------------------")

	m3u8_url_struct := get_url_struct(m3u8_url)
	m3u8_long_url = m3u8_url_struct.header + "://" + m3u8_url_struct.long_url
	m3u8_short_url = m3u8_url_struct.header + "://" + m3u8_url_struct.short_url

	resp, err := hc.Get(m3u8_url)
	if err != nil {
		panic(err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	resp.Body.Close()

	body_list := strings.Split(string(body), "\n")
	video_filename_list := list.New()
	video_map := &sync.Map{}

	ch := make(chan int)
	ch_valve := make(chan bool, task_count)

	ext_name := ""
	ix := 1

	m3u8_struct := parse_m3u8(body_list)

	if m3u8_struct.m3u8_type == INDEX {
		url_struct := get_url_struct(m3u8_struct.resource_list[0])
		url_type := url_struct.url_type
		url := ""
		if url_type == RELATIVE {
			url = m3u8_long_url + "/" + m3u8_struct.resource_list[0]
		} else if url_type == URL {
			url = m3u8_struct.resource_list[0]
		} else if url_type == ABSOLUTE {
			url = m3u8_short_url + m3u8_struct.resource_list[0]
		}

		resp, err := hc.Get(url)
		if err != nil {
			panic(err)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			panic(err)
		}

		resp.Body.Close()
		body_list = strings.Split(string(body), "\n")
		m3u8_struct = parse_m3u8(body_list)
		fmt.Println("meu8_index_url: " + m3u8_url)
		fmt.Println("meu8_content_url: " + url)
	} else {
		fmt.Println("meu8_content_url: " + m3u8_url)
	}

	if m3u8_struct.is_encrypt {
		fmt.Println("is_encrypt: yes")
		fmt.Println("encrypt_method: " + m3u8_struct.encrypt_method)
	} else {
		fmt.Println("is_encrypt: no")
	}

	resource_list := m3u8_struct.resource_list
	file_count := m3u8_struct.resource_count
	file_count_ = file_count
	ext_name = m3u8_struct.ext_name

	fmt.Printf("media_type: %s\n", ext_name)

	if limit > 0 {
		resource_list = resource_list[0:limit]
		file_count_ = limit
	}

	fmt.Printf("file count: %d\n", file_count_)

	for _, item := range resource_list {
		url_struct := get_url_struct(item)
		video_filename_list.PushBack(url_struct.file_name)
	}

	go create_file(output+"."+ext_name, video_map, *video_filename_list, ch)

	for _, body := range resource_list {
		line_struct := get_url_struct(body)
		url_type := line_struct.url_type
		file_name := line_struct.file_name

		url := ""
		if url_type == RELATIVE {
			url = m3u8_long_url + "/" + body
		} else if url_type == URL {
			url = body
		} else if url_type == ABSOLUTE {
			url = m3u8_short_url + body
		}
		ch_valve <- true
		go download_file(ix, url, video_map, file_name, 0, ch_valve, m3u8_struct.encrypt_key)
		ix++
	}

	<-ch
	fmt.Fprintf(os.Stdout, "100%%\r")
	fmt.Println()
}

func create_file(output_filename string, video_map *sync.Map, video_filename_list list.List, ch chan int) {
	output_file, err := os.Create(output_filename)

	if err != nil {
		panic(err)
	}

	ix := 0
	e := video_filename_list.Front()

	for {
		filename := e.Value.(string)
		buffer, flag := video_map.Load(filename)
		if flag {
			output_file, err = assembl_file(output_file, buffer.([]byte))
			if err != nil {
				panic(err)
			}
			video_map.Delete(filename)
			ix++
			e = e.Next()
		}

		if ix >= video_filename_list.Len() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	output_file.Close()
	ch <- 1
}

func download_file(ix int, url string, video_map *sync.Map, key string, count int, ch_valve chan bool, encrypt_key []byte) {
BEGIN:
	resp, err := hc.Get(url)
	if err != nil {
		fmt.Println(err)
		count++

		if count > retries_times {
			video_map.Store(key, make([]byte, 0))
			return
		} else {
			fmt.Println(url + " try: " + strconv.Itoa(count))
			goto BEGIN
		}
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if encrypt_key != nil {
		body = AES_CBC_Decrypt(body, encrypt_key)
	}

	video_map.Store(key, body)

	fmt.Fprintf(os.Stdout, "%d%%\r", downloaded_count*100/file_count_)
	<-ch_valve

	lock.Lock()
	downloaded_count++
	lock.Unlock()

}

func assembl_file(file *os.File, buffer []byte) (*os.File, error) {
	_, err := file.Write(buffer)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func get_url_struct(url string) UrlStruct {

	res := UrlStruct{}

	header := ""
	if strings.HasPrefix(url, "https") {
		header = "https"
	} else {
		header = "http"
	}

	res.header = header

	if strings.HasPrefix(url, "http") {
		res.url_type = URL
	} else if strings.HasPrefix(url, "/") {
		res.url_type = ABSOLUTE
	} else if !strings.HasPrefix(url, "/") {
		res.url_type = RELATIVE
	}

	re := regexp.MustCompile(`^https?://`)
	url_ := re.ReplaceAllString(url, "")
	arr := strings.Split(url_, "/")
	arr_len := len(arr)
	res.long_url = strings.Join(arr[0:arr_len-1], "/")
	res.short_url = arr[0]
	res.file_name = arr[arr_len-1]
	return res
}

func get_ext_name(line string) string {
	arr := strings.Split(line, ".")
	return arr[len(arr)-1]
}

func get_resource_list(content []string) []string {
	var res []string
	for _, item := range content {
		if !strings.HasPrefix(item, "#") {
			res = append(res, item)
		}
	}
	return res
}

func parse_m3u8(content []string) M3u8Struct {

	var res M3u8Struct

	res.is_encrypt = false
	res.encrypt_key = nil

	if len(content) < 10 {
		res.m3u8_type = INDEX
	} else {
		res.m3u8_type = RESOURCE
	}

	if res.m3u8_type == RESOURCE {
		encrypt_method, key := get_key(content)

		if encrypt_method != "" {
			res.is_encrypt = true
			res.encrypt_key = key
			res.encrypt_method = encrypt_method
		}
	}
	res.resource_list = get_resource_list(content)
	res.resource_count = len(res.resource_list)

	res.ext_name = get_ext_name(res.resource_list[0])

	return res
}

func get_key(content []string) (string, []byte) {
	encrypt_method := ""
	encrypt_key := new([]byte)
	for _, item := range content {
		if strings.HasPrefix(item, "#EXT-X-KEY") {

			re_method := regexp.MustCompile(`.*METHOD=([^,]+).*`)
			re_url := regexp.MustCompile(`.*"(.+)".*`)
			groups := re_method.FindStringSubmatch(item)
			if len(groups) == 2 {
				encrypt_method = groups[1]
			}

			groups = re_url.FindStringSubmatch(item)
			if len(groups) == 2 {
				encrypt_url := groups[1]
				encrypt_url_struct := get_url_struct(encrypt_url)

				if encrypt_url_struct.url_type == RELATIVE {
					encrypt_url = m3u8_long_url + "/" + encrypt_url
				} else if encrypt_url_struct.url_type == ABSOLUTE {
					encrypt_url = m3u8_short_url + encrypt_url
				}

				resp, err := hc.Get(encrypt_url)
				if err != nil {
					panic(err)
				}
				key, err := io.ReadAll(resp.Body)
				encrypt_key = &key
				if err != nil {
					fmt.Println(err)
					panic(err)
				}
			}
			break
		}
	}

	return encrypt_method, *encrypt_key

}

func AES_CBC_Decrypt(data []byte, key []byte) []byte {
	defer func() {
		if err := recover(); err != nil {
			fmt.Println("decrypt error")
		}
	}()

	block, _ := aes.NewCipher(key)
	blockSize := block.BlockSize()
	blockMode := cipher.NewCBCDecrypter(block, key[:blockSize])
	decrypted := make([]byte, len(data))
	blockMode.CryptBlocks(decrypted, data)
	decrypted = pkcs5UnPadding(decrypted)
	return decrypted
}

func pkcs5UnPadding(origData []byte) []byte {
	length := len(origData)
	unpadding := int(origData[length-1])
	return origData[:(length - unpadding)]
}
