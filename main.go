package main

import (
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"github.com/goincremental/negroni-sessions"
	"github.com/goincremental/negroni-sessions/cookiestore"
	_ "github.com/mattn/go-sqlite3"
	"github.com/urfave/negroni"
	"golang.org/x/crypto/bcrypt"
	"html/template"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
)

var db *sql.DB

type Book struct {
	PK             string
	Title          string `xml:"title,attr"`
	Author         string `xml:"author,attr"`
	Year           string `xml:"hyr,attr"`
	ID             string `xml:"owi,attr"`
	Classification string
}

type User struct {
	PK       string
	Username string
	Secret   string
}

type Page struct {
	User interface{}
}

type ClassifyResponse struct {
	Result []Book `xml:"works>work"`
}

type ClassifyBookResponse struct {
	BookData struct {
		Title  string `xml:"title,attr"`
		Author string `xml:"author,attr"`
		ID     string `xml:"owi,attr"`
	} `xml:"work"`
	Classification struct {
		MostPopular string `xml:"sfa,attr"`
	} `xml:"recommendations>ddc>mostPopular"`
}

var page *Page

func VerifyDatabase(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	if err := db.Ping(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	next(w, r)
}

func VerifyLogin(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	if r.URL.Path != "/auth/login" && r.URL.Path != "/favicon.ico" {
		user := sessions.GetSession(r).Get("user")
		page := &Page{}
		if user != nil {
			page.User = user
			next(w, r)
		} else {
			http.Redirect(w, r, "/auth/login", http.StatusFound)
		}
	} else {
		next(w, r)
	}
}

func main() {
	databaseSetup()
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var books []Book
		user := sessions.GetSession(r).Get("user")
		data, _ := db.Query("select pk, title, author, classification from books where books.user = ?", user)

		for data.Next() {
			var book Book
			data.Scan(&book.PK, &book.Title, &book.Author, &book.Classification)
			books = append(books, book)
		}

		tpl := template.Must(template.ParseFiles("templates/index.html"))
		tpl.Execute(w, books)

	})

	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		query := r.FormValue("search")
		xmlData, _ := http.Get("http://classify.oclc.org/classify2/Classify?&summary=true&title=" + url.QueryEscape(query))
		body, _ := ioutil.ReadAll(xmlData.Body)
		var c ClassifyResponse
		xml.Unmarshal(body, &c)
		json.NewEncoder(w).Encode(c.Result)
	})

	mux.HandleFunc("/books/add", func(w http.ResponseWriter, r *http.Request) {
		id := r.FormValue("id")
		xmlData, _ := http.Get("http://classify.oclc.org/classify2/Classify?&summary=true&owi=" + url.QueryEscape(id))
		data, _ := ioutil.ReadAll(xmlData.Body)
		user := sessions.GetSession(r).Get("user")
		var c ClassifyBookResponse
		xml.Unmarshal(data, &c)
		result, _ := db.Exec("INSERT into books(title, author, id, classification, user) values (?, ?, ?, ?, ?)", c.BookData.Title, c.BookData.Author, c.BookData.ID, c.Classification.MostPopular, user)

		pk, _ := result.LastInsertId()

		var book = Book{
			PK:             strconv.Itoa(int(pk)),
			Title:          c.BookData.Title,
			Author:         c.BookData.Author,
			Classification: c.Classification.MostPopular,
		}
		json.NewEncoder(w).Encode(book)
	})

	mux.HandleFunc("/books/delete", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		db.Exec("delete from books where pk = ?", id)
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/books", func(w http.ResponseWriter, r *http.Request) {
		query := r.FormValue("sortBy")

		var books []Book
		data, _ := db.Query("select pk, title, author, classification from books order by " + query)

		for data.Next() {
			var book Book
			data.Scan(&book.PK, &book.Title, &book.Author, &book.Classification)
			books = append(books, book)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Printf("%T", query)
		fmt.Println(sessions.GetSession(r).Get("sort-by"))
		sessions.GetSession(r).Set("sort-by", query)
		json.NewEncoder(w).Encode(books)
	})

	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("register") != "" {
			var s sql.NullString
			db.QueryRow("select * from users where username = ?", r.FormValue("username")).Scan(&s)

			if s.Valid {
				http.Redirect(w, r, "/auth/login", http.StatusFound)
			} else {
				secret, _ := bcrypt.GenerateFromPassword([]byte(r.FormValue("password")), bcrypt.DefaultCost)
				db.Exec("insert into users(username, secret) values (?, ?)", r.FormValue("username"), secret)
				sessions.GetSession(r).Set("user", r.FormValue("username"))
				http.Redirect(w, r, "/", http.StatusFound)
			}

		} else if r.FormValue("login") != "" {
			var user User
			userCheck := db.QueryRow("select * from users where username = ?", r.FormValue("username"))
			userCheck.Scan(&user.PK, &user.Username, &user.Secret)

			if err := bcrypt.CompareHashAndPassword([]byte(user.Secret), []byte(r.FormValue("password"))); err != nil {
				http.Redirect(w, r, "/auth/login", http.StatusFound)
			} else {
				http.Redirect(w, r, "/", http.StatusFound)
			}
		}
		tpl := template.Must(template.ParseFiles("templates/login.html"))
		tpl.Execute(w, nil)
	})

	ng := negroni.Classic()
	ng.Use(sessions.Sessions("go-for-web-dev", cookiestore.New([]byte("my-secret"))))
	ng.Use(negroni.HandlerFunc(VerifyLogin))
	ng.Use(negroni.HandlerFunc(VerifyDatabase))
	ng.UseHandler(mux)

	//http.ListenAndServe(":8000", ng)
	ng.Run(":8000")
}

func databaseSetup() {
	db, _ = sql.Open("sqlite3", "book.db")
	db.Exec("CREATE TABLE IF NOT EXISTS books(pk integer primary key AUTOINCREMENT, title text, author text, id text, classification text)")
	db.Exec("CREATE TABLE IF NOT EXISTS users(pk integer primary key AUTOINCREMENT, username text, secret text)")
}
