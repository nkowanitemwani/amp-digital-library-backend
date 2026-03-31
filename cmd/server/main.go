package main

import (

	"log"

	"github.com/joho/godotenv"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/config"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/db"

)



func main() {

	//get enviroment variable from .env file
	godotenv.Load("../../.env")

	//pass down environment variable
	cfg :=config.Load()

	//Initialize Database
	db,err := db.InitializeDatabase(cfg);
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	
}
