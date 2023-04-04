package openai

import (
	"context"
	"fmt"
	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/fvbock/endless"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"
	"io"
	"time"

	"github.com/gin-gonic/gin"
)

// Ported over from: https://github.com/acheong08/ChatGPT-Proxy-V4

type ChatGPTProxy struct {
	jar         tls_client.CookieJar
	options     []tls_client.HttpClientOption
	accessToken string
	client      tls_client.HttpClient
	puid        string
	host        string
	port        uint16
}

type ChatGPTProxyOption func(*ChatGPTProxy)

func WithAccessToken(accessToken string) ChatGPTProxyOption {
	return func(p *ChatGPTProxy) {
		p.accessToken = accessToken
	}
}

func WithPUID(puid string) ChatGPTProxyOption {
	return func(p *ChatGPTProxy) {
		p.puid = puid
	}
}

func WithHost(host string) ChatGPTProxyOption {
	return func(p *ChatGPTProxy) {
		p.host = host
	}
}

func WithPort(port uint16) ChatGPTProxyOption {
	return func(p *ChatGPTProxy) {
		p.port = port
	}
}

func NewChatGPTProxy(options ...ChatGPTProxyOption) (*ChatGPTProxy, error) {
	jar := tls_client.NewCookieJar()
	tlsOptions := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(360),
		tls_client.WithClientProfile(tls_client.Chrome_110),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCookieJar(jar), // create cookieJar instance and pass it as argument
	}
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), tlsOptions...)
	if err != nil {
		return nil, err
	}
	ret := &ChatGPTProxy{
		jar:     jar,
		options: tlsOptions,
		client:  client,
	}

	for _, option := range options {
		option(ret)
	}

	return ret, nil
}

// RunRefreshPUIDCookie refreshes the _puid cookie every 6 hours to prevent it from expiring
func (p *ChatGPTProxy) RunRefreshPUIDCookie(ctx context.Context) error {
	url := "https://chat.openai.com/backend-api/models"
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Host", "chat.openai.com")
	req.Header.Set("origin", "https://chat.openai.com/chat")
	req.Header.Set("referer", "https://chat.openai.com/chat")
	req.Header.Set("sec-ch-ua", `Chromium";v="110", "Not A(Brand";v="24", "Brave";v="110`)
	req.Header.Set("sec-ch-ua-platform", "Linux")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "text/event-stream")
	req.Header.Set("user-agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/110.0.0.0 Safari/537.36")
	// Set authorization header
	req.Header.Set("Authorization", "Bearer "+p.accessToken)
	// Initial puid cookie
	req.AddCookie(
		&http.Cookie{
			Name:  "_puid",
			Value: p.puid,
		},
	)
	for {
		log.Debug().Msg("Refreshing puid cookie")
		resp, err := p.client.Do(req)
		if err != nil {
			break
		}
		defer func(Body io.ReadCloser) {
			_ = Body.Close()
		}(resp.Body)

		log.Debug().Str("status", resp.Status).Msg("Got response")

		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			log.Error().
				Str("status", resp.Status).
				Str("body", string(body)).
				Msg("Got error response")
			break
		}

		cookies := resp.Cookies()

		// Find _puid cookie
		for _, cookie := range cookies {
			if cookie.Name == "_puid" {
				p.puid = cookie.Value
				log.Debug().Str("puid", p.puid).Msg("Got new puid")
				break
			}
		}

		// Sleep for 6 hour before refreshing the cookie, handling context cancellation
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(6 * time.Hour):
		}
	}

	return fmt.Errorf("Failed to refresh puid cookie")
}

func (p *ChatGPTProxy) Handle(c *gin.Context) error {
	var url string
	var err error
	var request_method string
	var request *http.Request
	var response *http.Response

	url = "https://chat.openai.com/backend-api" + c.Param("path")
	request_method = c.Request.Method

	request, err = http.NewRequest(request_method, url, c.Request.Body)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return err
	}
	request.Header.Set("Host", "chat.openai.com")
	request.Header.Set("Origin", "https://chat.openai.com/chat")
	request.Header.Set("Connection", "keep-alive")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Keep-Alive", "timeout=360")
	request.Header.Set("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/87.0.4280.88 Safari/537.36")
	request.Header.Set("Authorization", c.Request.Header.Get("Authorization"))
	if c.Request.Header.Get("Puid") == "" {
		request.AddCookie(
			&http.Cookie{
				Name:  "_puid",
				Value: p.puid,
			},
		)
	} else {
		request.AddCookie(
			&http.Cookie{
				Name:  "_puid",
				Value: c.Request.Header.Get("Puid"),
			},
		)
	}

	response, err = p.client.Do(request)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return err
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(response.Body)

	c.Header("Content-Type", response.Header.Get("Content-Type"))
	// Get status code
	c.Status(response.StatusCode)
	c.Stream(func(w io.Writer) bool {
		// Write data to client
		_, err := io.Copy(w, response.Body)
		if err != nil {
			return false
		}
		return false
	})

	return nil
}

func (p *ChatGPTProxy) Run(ctx context.Context) error {
	handler := gin.Default()
	handler.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "pong"})
	})

	handler.Any("/api/*path", func(c *gin.Context) {
		err := p.Handle(c)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
		}
	})

	err := endless.ListenAndServe(fmt.Sprintf("%s:%d", p.host, p.port), handler)
	if err != nil {
		return err
	}

	return nil
}

func NewProxyCommand() (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Proxy for OpenAI Chat GPT",
		Run: func(cmd *cobra.Command, args []string) {
			accessToken := viper.GetString("access-token")
			puid := viper.GetString("puid")

			if accessToken == "" && puid == "" {
				cobra.CheckErr(fmt.Errorf("ACCESS_TOKEN and PUID are not set"))
			}

			port, err := cmd.Flags().GetUint16("port")
			cobra.CheckErr(err)
			host, err := cmd.Flags().GetString("host")
			cobra.CheckErr(err)

			proxy, err := NewChatGPTProxy(
				WithAccessToken(accessToken),
				WithPUID(puid),
				WithPort(port),
				WithHost(host),
			)
			cobra.CheckErr(err)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			eg := errgroup.Group{}

			eg.Go(func() error {
				return proxy.Run(ctx)
			})

			if accessToken != "" {
				eg.Go(func() error {
					return proxy.RunRefreshPUIDCookie(ctx)
				})
			}

			err = eg.Wait()
			cobra.CheckErr(err)
		},
	}

	cmd.Flags().String("access-token", "", "OpenAI Chat GPT access token")
	cmd.Flags().String("puid", "", "OpenAI Chat GPT puid")
	cmd.Flags().Uint16("port", 8080, "Port to listen on")
	cmd.Flags().String("host", "localhost", "Host to listen on")

	err := viper.BindPFlags(cmd.Flags())
	if err != nil {
		return nil, err
	}

	return cmd, nil
}
