{ pkgs, lib, config, inputs, ... }:

{
  packages = [
    pkgs.nodejs_22
  ];

  languages.typescript.enable = true;
  languages.go.enable = true;

  enterShell = ''
  '';
}
