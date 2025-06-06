package lib

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"strconv"
	"time"
	"sync"
	webdav "webdav/lib_official_webdav"
	"gopkg.in/h2non/bimg.v1"
	"os"
	"os/exec"
	"path/filepath"
)

// CorsCfg is the CORS config.
type CorsCfg struct {
	Enabled        bool
	Credentials    bool
	AllowedHeaders []string
	AllowedHosts   []string
	AllowedMethods []string
	ExposedHeaders []string
}


var mutex = &sync.Mutex{}

// Config is the configuration of a WebDAV instance.
type Config struct {
	NoSniff bool
	Cors    CorsCfg
	Users   map[string]*User
}

// Allowed checks if the user on the scope has permission to access a directory/file
func checkPerm(s *Scope, path string, isWrite bool) bool {

	for _, rule := range s.Rules {
		isAllowed := (isWrite && rule.Allow_w) || (!isWrite && rule.Allow_r)

		if rule.Regex {
			if rule.Regexp.MatchString(path) {
				return isAllowed
			}
		} else if strings.HasPrefix(path, rule.Path) {
			return isAllowed
		}
	}

	return !isWrite || (isWrite && s.Allow_w)
}

func findScope(u *User, url string) (s *Scope, path string, absolutePath string, owncloud bool) {
	for alias, scope := range u.Scopes {
		if strings.HasPrefix(url, alias) {
			s = scope
			path = strings.TrimPrefix(url, scope.Handler.Prefix)
			absolutePath = strings.Join([]string{s.Root, path}, "")
			owncloud = scope.Owncloud
			return
		}
	}
	return
}

func returnRoots(w http.ResponseWriter, r *http.Request, u *User) error {
	ctx := r.Context()
	mw := webdav.MultistatusWriter{Writer: w}
	for alias, scope := range u.Scopes {
		h := scope.Handler
		pf, _, err := webdav.ReadPropfind(r.Body)
		if err != nil {
			return err
		}

		var pstats []webdav.Propstat
		pstats, err = webdav.Allprop(ctx, h.FileSystem, h.LockSystem, "/", pf.Prop)
		err = mw.Write(webdav.MakePropstatResponse(alias, pstats))
		if err != nil {
			return err
		}
	}

	err := mw.Close()
	if err != nil {
		return err
	}

	return nil
}

// ServeHTTP determines if the request is for this plugin, and if all prerequisites are met.
func (c *Config) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestOrigin := r.Header.Get("Origin")

	// Add CORS headers before any operation so even on a 401 unauthorized status, CORS will work.
	if c.Cors.Enabled && requestOrigin != "" {
		headers := w.Header()

		allowedHeaders := strings.Join(c.Cors.AllowedHeaders, ", ")
		allowedMethods := strings.Join(c.Cors.AllowedMethods, ", ")
		exposedHeaders := strings.Join(c.Cors.ExposedHeaders, ", ")

		allowAllHosts := len(c.Cors.AllowedHosts) == 1 && c.Cors.AllowedHosts[0] == "*"
		allowedHost := isAllowedHost(c.Cors.AllowedHosts, requestOrigin)

		if allowAllHosts {
			headers.Set("Access-Control-Allow-Origin", "*")
		} else if allowedHost {
			headers.Set("Access-Control-Allow-Origin", requestOrigin)
		}

		if allowAllHosts || allowedHost {
			headers.Set("Access-Control-Allow-Headers", allowedHeaders)
			headers.Set("Access-Control-Allow-Methods", allowedMethods)

			if c.Cors.Credentials {
				headers.Set("Access-Control-Allow-Credentials", "true")
			}

			if len(c.Cors.ExposedHeaders) > 0 {
				headers.Set("Access-Control-Expose-Headers", exposedHeaders)
			}
		}
	}

	if r.Method == "OPTIONS" && c.Cors.Enabled && requestOrigin != "" {
		return
	}

	/** Authentication */

	w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)

	// Gets the correct user for this request.
	username, password, ok := r.BasicAuth()
	reqHost := func() string {
		hArr := strings.Split(r.RemoteAddr, ":")
		return hArr[0]
	}
	reqMark := fmt.Sprintf("%s:%s", reqHost(), username)

	if _, found := authorizedSource[reqMark]; !found {
		log.Printf("%s tried to verify account , username is [%s]", r.RemoteAddr, username)
	}

	if strings.Contains(r.URL.Path, "status.php") {
	    ver := []byte(`{"edition":"","installed":true,"maintenance":false,"needsDbUpgrade":false,"productname":"Nextcloud","version":"16.0.0.9","versionstring":"16.0.0"}`)
	    w.Header().Set("Content-Type", "application/json")
	    w.Write(ver)
	    return
	}

	if !ok {
		http.Error(w, "Not authorized", 401)
		return
	}

	user, ok := c.Users[username]
	if !ok {
		http.Error(w, "Not authorized", 401)
		return
	}

	if !checkPassword(user.Password, password) {
		log.Println("Wrong Password for user", username)
		http.Error(w, "Not authorized", 401)
		return
	} else {
		mutex.Lock()
		authorizedSource[reqMark] = time.Now()
		mutex.Unlock()
	}

	isValid := r.Method == "GET" ||
		r.Method == "HEAD" ||
		r.Method == "OPTIONS" ||
		r.Method == "PROPFIND" ||
		r.Method == "PROPPATCH" ||
		r.Method == "POST" ||
		r.Method == "PUT" ||
		r.Method == "LOCK" ||
		r.Method == "UNLOCK" ||
		r.Method == "COPY" ||
		r.Method == "MOVE" ||
		r.Method == "MKCOL" ||
		r.Method == "DELETE"

	if !isValid {
		log.Printf("Invalid request: [%s] %s %s", user.Username, r.Method, r.URL.Path)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	isWrite := r.Method == "PUT" ||
		r.Method == "PROPPATCH" ||
		r.Method == "LOCK" ||
		r.Method == "UNLOCK" ||
		r.Method == "COPY" ||
		r.Method == "MOVE" ||
		r.Method == "MKCOL" ||
		r.Method == "DELETE"

	//请求根目录
	if r.URL.Path == "/" {
		//根目录是虚拟目录，不可写
		if isWrite {
			log.Printf("Not Allowed: [%s] %s %s", user.Username, r.Method, r.URL.Path)
			w.WriteHeader(http.StatusForbidden)
			return
		} else {
			err := returnRoots(w, r, user)
			if err != nil {
				log.Printf("Can't get roots")
				w.WriteHeader(http.StatusNotFound)
			} else {
				log.Printf("[%s] %s /", user.Username, r.Method)
			}
			return
		}
	}

	//查找请求路径所属Scope
	scope, path, absolutePath, owncloud := findScope(user, r.URL.Path)

	if scope == nil {
		log.Printf("Unknown scope: [%s] %s %s", user.Username, r.Method, r.URL.Path)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	//权限校验
	if !checkPerm(scope, path, isWrite) {
		log.Printf("Not Allowed: [%s] %s %s", user.Username, r.Method, absolutePath)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	if r.Method == "HEAD" {
		w = newResponseWriterNoBody(w)
	}

	if r.Method == "GET" && owncloud {
	    file := r.URL.Query().Get("file")

	    width, err := strconv.Atoi(r.URL.Query().Get("x"))
	    if err != nil {
		log.Printf("Error: %s", err)
		width = 512
	    }

	    height, err := strconv.Atoi(r.URL.Query().Get("y"))
	    if err != nil {
		log.Printf("Error: %s", err)
		height = 512
	    }

	    img_path := absolutePath + file
	    ext := filepath.Ext(file)
	    if ext != "" {
		ext = ext[1:]
	    }

	    if ext == "mp4" {
		filter := fmt.Sprintf("thumbnail,select='gte(t,2)',scale=%d:-1", width)
		tmpFile, err := os.CreateTemp("", "videothumb-*")
		if err != nil {
			log.Printf("Video thumbnail error: %s", err)
		}
		thumbName := tmpFile.Name()
		tmpFile.Close()
		os.Remove(thumbName)
		thumbName = thumbName + ".webp"
		ffcmd := exec.Command("ffmpeg", "-i", img_path, "-vf", filter, "-frames:v", "1", thumbName)
		//ffcmd.Stdout = os.Stdout
		//ffcmd.Stderr = os.Stderr
		err = ffcmd.Run()
		if err != nil {
			log.Printf("Video thumbnail error: %s", err)
			return
		}
		pix, err := bimg.Read(thumbName)
		if err != nil {
			log.Printf("Error: %s", err)
			return
		}
		w.Header().Set("Content-Type", "image/webp")
		w.Write(pix)
		os.Remove(thumbName)
		return
	    } else {
		options := bimg.Options{
			Width: width,
			Height: height,
			Crop: true,
			Quality: 65,
			Type: bimg.WEBP,
			Interpolator: bimg.Nearest,
			StripMetadata: true,
			Interlace: true,
		}
		pix, err := bimg.Read(img_path)
		if err != nil {
			log.Printf("Error: %s", err)
			return
		}
		newPix, err := bimg.Resize(pix, options)
		if err != nil {
			log.Printf("Error: %s", err)
			return
		}
		w.Header().Set("Content-Type", "image/webp")
		w.Write(newPix)
		return
	    }
	}

	// Excerpt from RFC4918, section 9.4:
	//
	// 		GET, when applied to a collection, may return the contents of an
	//		"index.html" resource, a human-readable view of the contents of
	//		the collection, or something else altogether.
	//
	// Get, when applied to collection, will return the same as PROPFIND method.
	if r.Method == "GET" {
		info, err := scope.Handler.FileSystem.Stat(context.TODO(), path)
		if err == nil && info.IsDir() {
			r.Method = "PROPFIND"

			if r.Header.Get("Depth") == "" {
				r.Header.Add("Depth", "1")
			}
		}
	}

	// Runs the WebDAV.
	//u.Handler.LockSystem = lib_official_webdav.NewMemLS()
	scope.Handler.ServeHTTP(w, r)
}

// responseWriterNoBody is a wrapper used to suprress the body of the response
// to a request. Mainly used for HEAD requests.
type responseWriterNoBody struct {
	http.ResponseWriter
}

// newResponseWriterNoBody creates a new responseWriterNoBody.
func newResponseWriterNoBody(w http.ResponseWriter) *responseWriterNoBody {
	return &responseWriterNoBody{w}
}

// Header executes the Header method from the http.ResponseWriter.
func (w responseWriterNoBody) Header() http.Header {
	return w.ResponseWriter.Header()
}

// Write suprresses the body.
func (w responseWriterNoBody) Write(data []byte) (int, error) {
	return 0, nil
}

// WriteHeader writes the header to the http.ResponseWriter.
func (w responseWriterNoBody) WriteHeader(statusCode int) {
	w.ResponseWriter.WriteHeader(statusCode)
}
