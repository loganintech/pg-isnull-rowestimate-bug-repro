{
  description = "PostgreSQL 18 EXPLAIN ANALYZE demo — duplicate IS NULL selectivity";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };

        pg-test = pkgs.buildGoModule {
          pname = "pg-test";
          version = "0.1.0";
          src = ./.;
          # Pinned; update by setting to pkgs.lib.fakeHash and reading the error.
          vendorHash = "sha256-zvkAsnQhOAJjexGaFGRX1eHoKb1s9vLg2NFzraLWDKY=";
          # The program shells out to `docker` at runtime (not a build input).
          doCheck = false;
        };
      in
      {
        packages.default = pg-test;

        apps.default = {
          type = "app";
          program = "${pg-test}/bin/pg-test";
        };

        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.go            # 1.26.4, matches go.mod
            pkgs.gopls
            pkgs.gotools
            pkgs.postgresql_18 # provides `psql` for poking at the DB (optional)
          ];

          shellHook = ''
            echo "pg-test dev shell — Go $(go version | awk '{print $3}')"
            echo "Prerequisite: a running Docker daemon (Docker Desktop, colima, etc.)."
            echo "Run the demo with:  go run .   (or: nix run)"
          '';
        };
      });
}
