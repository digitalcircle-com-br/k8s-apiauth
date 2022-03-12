package main

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/digitalcircle-com-br/buildinfo"
	n "github.com/digitalcircle-com-br/nanoapi"
	nanoapigorm "github.com/digitalcircle-com-br/nanoapi-gorm"
	nanoapiperm "github.com/digitalcircle-com-br/nanoapi-perm"
	nanoapiredis "github.com/digitalcircle-com-br/nanoapi-redis"
	nanoapisession "github.com/digitalcircle-com-br/nanoapi-session"
	nanoapisessionredis "github.com/digitalcircle-com-br/nanoapi-session-redis"
	"github.com/digitalcircle-com-br/random"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func main() {
	log.Printf(buildinfo.String())

	err := n.SetupWDeps(
		nanoapigorm.Setup,
		nanoapiredis.Setup,
		nanoapisession.Setup,
		nanoapisessionredis.Setup,
		nanoapiperm.Setup,
		Setup,
	)
	if err != nil {
		log.Fatal(err.Error())
	}

	log.Fatal(http.ListenAndServe(":80", n.Mux()))
}

type VO struct {
	ID uint `json:"id"`
}

type SecUser struct {
	VO
	Username string
	Password string
	Email    string
	Groups   []SecGroup `gorm:"many2many:sec_user_groups;"`
	Enabled  *bool
	Tenant   string
}

type SecGroup struct {
	VO
	Code  string
	Perms []SecPerm `gorm:"many2many:sec_group_perms;"`
}

type SecPerm struct {
	VO
	Code string
	Perm string
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func Setup() error {

	var err error
	var db *gorm.DB
	db = nanoapigorm.DB()
	if db == nil {
		return nanoapigorm.ErrDBCantBeNil
	}

	for _, v := range []interface{}{
		&SecUser{},
		&SecGroup{},
		&SecPerm{},
	} {
		err := db.AutoMigrate(v)
		if err != nil {
			return err
		}
	}

	err = db.First(&SecUser{}).Where("username = 'root'").Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		perm := &SecPerm{
			Code: "*",
			Perm: "*",
		}
		err = db.Save(perm).Error
		if err != nil {
			return err
		}
		group := &SecGroup{Code: "root", Perms: []SecPerm{*perm}}
		err = db.Save(group).Error
		if err != nil {
			return err
		}
		pass, err := bcrypt.GenerateFromPassword([]byte("Aa1234"), 10)
		if err != nil {
			return err
		}

		ptrTrue := true
		user := &SecUser{Username: "root", Password: string(pass), Groups: []SecGroup{*group}, Enabled: &ptrTrue, Tenant: "root"}
		err = db.Save(user).Error
		if err != nil {
			return err
		}
	}

	n.Post("/login", func(ctx context.Context, in LoginRequest) (string, error) {
		//req := n.CtxReq(ctx)
		rw := n.CtxRes(ctx)

		secuser := &SecUser{}
		err := db.Preload("Groups.Perms").Preload(clause.Associations).Where("username = ? and enabled = true", in.Username).First(secuser).Error
		if err != nil {
			return "", err
		}
		err = bcrypt.CompareHashAndPassword([]byte(secuser.Password), []byte(in.Password))
		if err != nil {
			rw.WriteHeader(http.StatusUnauthorized)
			n.CtxDone(ctx)()
			return "", nil
		}
		sess := nanoapisession.Session{}
		sess.User = secuser.Username
		sess.Tenant = secuser.Tenant
		sess.Perms = make(map[string]string)

		for _, g := range secuser.Groups {
			for _, perm := range g.Perms {
				sess.Perms[perm.Code] = perm.Perm
			}
		}

		sess.ID = random.StrTS(32)
		err = nanoapisession.SessionSave(ctx, sess)

		if err != nil {
			return "", err
		}
		http.SetCookie(rw, &http.Cookie{Name: "SESSION", Value: sess.ID, Path: "/", Domain: ".digitalcircle.com.br", Secure: true, MaxAge: 60 * 60 * 24 * 365, HttpOnly: true, SameSite: http.SameSiteNoneMode})

		return "ok", nil
	}, &n.Opts{Perm: n.PERM_ALL})

	n.Get("/check", func(ctx context.Context) (string, error) {
		sess := nanoapisession.CtxSession(ctx)
		if sess != nil {
			return "ok", nil
		}
		rw := n.CtxRes(ctx)
		rw.WriteHeader(http.StatusUnauthorized)
		n.CtxDone(ctx)()
		return "", nil

	}, &n.Opts{Perm: n.PERM_AUTH})

	n.Get("/logout", func(ctx context.Context) (string, error) {
		sessid := nanoapisession.CtxSessionID(ctx)
		if sessid != "" {
			nanoapisession.SessionDel(ctx, sessid)
			rw := n.CtxRes(ctx)

			http.SetCookie(rw, &http.Cookie{Name: "SESSION", Domain: ".digitalcircle.com.br", Value: "", Secure: true, MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteNoneMode})

		}

		return "ok", nil
	}, &n.Opts{Perm: n.PERM_AUTH})

	n.Get("/perms", func(ctx context.Context) (map[string]string, error) {
		s := nanoapisession.CtxSession(ctx)
		if s == nil {
			return nil, nil
		}
		return s.Perms, nil
	}, &n.Opts{Perm: n.PERM_AUTH})

	n.Get("/me", func(ctx context.Context) (string, error) {
		s := nanoapisession.CtxSession(ctx)
		if s == nil {
			return "", nil
		}
		return s.User, nil
	}, &n.Opts{Perm: n.PERM_AUTH})

	n.Get("/tenant", func(ctx context.Context) (string, error) {
		s := nanoapisession.CtxSession(ctx)
		if s == nil {
			return "", nil
		}
		return s.Tenant, nil
	}, &n.Opts{Perm: n.PERM_AUTH})

	return nil

}
