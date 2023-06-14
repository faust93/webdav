# WebDav Server
* Adapted from [Travalone/webdav](https://github.com/Travalone/webdav)

Webdav server with partial Nextcloud DAV emulation layer (image previews/thumbnails generation mainly)

To be used with image gallerys with NextCloud storage support, like:
* QuickPic (Android)
* Piktures (Android)

To emulate Nextcloud DAV the following entries must be configured:
```yaml
  - username: owncloud
    password: owncloud123
    scopes:
      - root: /srv/dav/Photos
        alias: remote.php/webdav
        allow_w: true
      - root: /srv/dav/Photos
        alias: remote.php/dav/files/owncloud
        allow_w: true
      - root: /srv/dav/Photos
        alias: /index.php/core/preview.png
        owncloud_preview: true
```

See `config.yml` for configuration example

## Building

1. Clone webdav repo:
```bash
git clone https://github.com/faust93/webdav.git
```

2. Install dependencies:
```bash
$ yay -S go libvips openslide
```

3. Build the app:
```bash
cd ./webdav
go build
```

### Original README.md

* Adapted from [hacdias/webdav](https://github.com/hacdias/webdav)
* 按个人习惯修改了配置选项，参考FileZilla

## Changes
* 认证和默认用户配置选项删除，必须在users中配置用户
* 支持单个用户下配置多个路径
    * 伪造虚拟根目录，返回每个路径的别名
    * 利用prefix实现，所以配置文件中不再开放prefix选项
* 权限参数改为allow_r和allow_w
    * 用户可配置写权限，默认不可写
    * 每个路径可配置读写权限，覆盖用户权限
* 获取文件列表时跳过无权访问项
    * 原本golang webdav库的walkFS函数遇到无权访问的目录会停止扫描、报错，即使还有部分可访问项尚未扫描
    * Windows下返回syscall.ERROR_ACCESS_DENIED，这里修改为跳过，继续扫描
* 直接把golang的webdav库拉到项目中做了改动
    * 伪造根目录需要使用webdav库的部分私有方法，仅将其改为公有
    * 跳过无权访问项需要判断错误类型以实现跳过
* 修改了部分文件名，避免重复

## Problems
* 没有配置参数校验
* walkFS函数扫描到无权访问项时返回的错误类型与OS有关，实测Mac下会报错

## 配置文件示例
```
# Server related settings
address: 0.0.0.0
port: 1111
tls: false
cert: cert.pem
key: key.pem

# CORS configuration
cors:
  enabled: true
  credentials: true
  allowed_headers:
    - Depth
  allowed_hosts:
    - http://localhost:1111
  allowed_methods:
    - GET
  exposed_headers:
    - Content-Length
    - Content-Range

# 密码可以到https://bcrypt-generator.com/生成
# 然后添加"{bcrypt}"前缀
users:
  - username: webdav
    password: 123456
    scopes:
      - root: E:/
        alias: pce
        rules:
          - path: /Downloads
            allow_w: true
      - root: F:/
        alias: pcf
        rules:
          - path: /Downloads
            allow_r: false

```